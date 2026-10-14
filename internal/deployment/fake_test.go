package deployment

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moureau-dev/menina/internal/kube"
)

// fakeAPI is a tiny in-memory Kubernetes API server covering the subset Menina
// uses: get/list/create/update/delete on a collection, label-selector lists,
// /version, and service-account token secret population.
type fakeAPI struct {
	mu    sync.Mutex
	store map[string]map[string]map[string]any
	srv   *httptest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{store: map[string]map[string]map[string]any{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) cluster(t *testing.T) *KubernetesCluster {
	t.Helper()
	client, err := newFakeClient(f)
	if err != nil {
		t.Fatal(err)
	}
	return NewKubernetesClusterFromClient(client)
}

func newFakeClient(f *fakeAPI) (*kube.Client, error) {
	return kube.NewInsecureClient(f.srv.URL)
}

func (f *fakeAPI) seed(collection, name string, obj map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.store[collection] == nil {
		f.store[collection] = map[string]map[string]any{}
	}
	meta := ensureMeta(obj)
	meta["name"] = name
	meta["resourceVersion"] = "1"
	f.store[collection][name] = obj
}

func (f *fakeAPI) object(collection, name string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.store[collection]; m != nil {
		return m[name]
	}
	return nil
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/version" {
		writeJSON(w, http.StatusOK, map[string]any{"gitVersion": "v1.30.0"})
		return
	}
	collection, name, ok := parseK8sPath(r.URL.Path)
	if !ok {
		writeStatus(w, http.StatusNotFound, "NotFound", "unknown path")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if name == "" {
			items := f.listLocked(collection, r.URL.Query().Get("labelSelector"))
			writeJSON(w, http.StatusOK, map[string]any{"kind": "List", "items": items})
			return
		}
		obj := f.getLocked(collection, name)
		if obj == nil {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		writeJSON(w, http.StatusOK, obj)
	case http.MethodPost:
		var obj map[string]any
		_ = json.NewDecoder(r.Body).Decode(&obj)
		meta := ensureMeta(obj)
		n, _ := meta["name"].(string)
		if n == "" {
			writeStatus(w, http.StatusBadRequest, "BadRequest", "missing name")
			return
		}
		if f.getLocked(collection, n) != nil {
			writeStatus(w, http.StatusConflict, "AlreadyExists", "already exists")
			return
		}
		meta["resourceVersion"] = "1"
		meta["uid"] = "uid-" + n
		f.populateSecret(obj)
		f.setLocked(collection, n, obj)
		writeJSON(w, http.StatusCreated, obj)
	case http.MethodPut:
		var obj map[string]any
		_ = json.NewDecoder(r.Body).Decode(&obj)
		if f.getLocked(collection, name) == nil {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		meta := ensureMeta(obj)
		meta["name"] = name
		f.setLocked(collection, name, obj)
		writeJSON(w, http.StatusOK, obj)
	case http.MethodDelete:
		if f.getLocked(collection, name) == nil {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		delete(f.store[collection], name)
		writeJSON(w, http.StatusOK, map[string]any{"kind": "Status", "status": "Success"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeAPI) getLocked(collection, name string) map[string]any {
	if m := f.store[collection]; m != nil {
		return m[name]
	}
	return nil
}

func (f *fakeAPI) setLocked(collection, name string, obj map[string]any) {
	if f.store[collection] == nil {
		f.store[collection] = map[string]map[string]any{}
	}
	f.store[collection][name] = obj
}

func (f *fakeAPI) listLocked(collection, selector string) []any {
	var out []any
	for coll, objs := range f.store {
		if !sameCollection(coll, collection) {
			continue
		}
		for _, obj := range objs {
			if matchSelector(obj, selector) {
				out = append(out, obj)
			}
		}
	}
	return out
}

func sameCollection(stored, requested string) bool {
	if stored == requested {
		return true
	}
	// all-namespaces list: /prefix/resource matches /prefix/namespaces/{ns}/resource
	i := strings.LastIndex(requested, "/")
	if i < 0 {
		return false
	}
	prefix, resource := requested[:i], requested[i+1:]
	return strings.HasPrefix(stored, prefix+"/namespaces/") && strings.HasSuffix(stored, "/"+resource)
}

func matchSelector(obj map[string]any, selector string) bool {
	if selector == "" {
		return true
	}
	meta, _ := obj["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)
	for _, part := range strings.Split(selector, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if labels == nil || labels[kv[0]] != kv[1] {
			return false
		}
	}
	return true
}

func (f *fakeAPI) populateSecret(obj map[string]any) {
	if t, _ := obj["type"].(string); t != "kubernetes.io/service-account-token" {
		return
	}
	data, _ := obj["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		obj["data"] = data
	}
	data["token"] = base64.StdEncoding.EncodeToString([]byte("fake-sa-token"))
	data["ca.crt"] = base64.StdEncoding.EncodeToString([]byte("fake-ca"))
}

func ensureMeta(obj map[string]any) map[string]any {
	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		obj["metadata"] = meta
	}
	return meta
}

func parseK8sPath(path string) (collection, name string, ok bool) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 {
		return "", "", false
	}
	var prefixLen int
	switch segs[0] {
	case "api":
		prefixLen = 2
	case "apis":
		prefixLen = 3
	default:
		return "", "", false
	}
	if len(segs) <= prefixLen {
		return "", "", false
	}
	prefix := "/" + strings.Join(segs[:prefixLen], "/")
	rest := segs[prefixLen:]
	if rest[0] == "namespaces" {
		if len(rest) == 1 {
			return prefix + "/namespaces", "", true
		}
		if len(rest) == 2 {
			return prefix + "/namespaces/" + rest[1], "", true
		}
		coll := prefix + "/namespaces/" + rest[1] + "/" + rest[2]
		if len(rest) >= 4 {
			return coll, rest[3], true
		}
		return coll, "", true
	}
	coll := prefix + "/" + rest[0]
	if len(rest) >= 2 {
		return coll, rest[1], true
	}
	return coll, "", true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeStatus(w http.ResponseWriter, code int, reason, message string) {
	writeJSON(w, code, map[string]any{"kind": "Status", "code": code, "reason": reason, "message": message})
}
