/* ---------- External ---------- */
import Newstack from "@moureau/newstack";

/* ---------- Components ---------- */
import { CopyCommand } from "./CopyCommand";

const GITHUB = "https://github.com/moureau-dev/menina";
const MOUREAU = "https://moureau.dev";

const features = [
  {
    title: "Self-hosted by design",
    body: "Runs on your VPS. Your servers, your configuration and your data stay yours. Nothing depends on someone else's cloud.",
  },
  {
    title: "Push to deploy",
    body: "Pushing an image to the bundled registry triggers a release. Menina matches the repository and tag to your apps and reconciles the workload.",
  },
  {
    title: "k3s underneath",
    body: "Workloads run on Kubernetes with Traefik and ServiceLB disabled. You get real orchestration without having to live in Kubernetes every day.",
  },
  {
    title: "Immutable releases",
    body: "Every tag is resolved to an image digest. Releases are recorded, so you can roll back to a version that already ran.",
  },
  {
    title: "Routing and HTTPS",
    body: "A built-in reverse proxy routes public hostnames and issues per-hostname Let's Encrypt certificates. Raw TCP and UDP work too, for services that are not HTTP.",
  },
  {
    title: "Configuration as code",
    body: "Desired state is human-editable YAML under /etc/menina, and operational state lives in SQLite. Everything is plain text you can read and put in git.",
  },
  {
    title: "Environments and scaling",
    body: "Model production, staging and more with per-environment overrides. Scale with a fixed number of replicas or an autoscaling range.",
  },
  {
    title: "Backups and upgrades",
    body: "Portable tar.gz backups (config, database, volumes, images) and a self-upgrade that verifies the checksum before it swaps the binary.",
  },
];

const steps = [
  {
    n: "01",
    title: "Install and provision",
    body: "The installer only installs the binary and systemd units. Then menina setup writes the config, and menina deploy provisions k3s, the registry, the control plane and the proxy.",
  },
  {
    n: "02",
    title: "Define your apps",
    body: "Projects, environments and apps are plain YAML under /etc/menina. You can also use the guided CLI and its interactive menu.",
  },
  {
    n: "03",
    title: "Push an image",
    body: "Log in to registry.<domain> and push host/<project>/<app>:<tag> from any build machine or CI pipeline.",
  },
  {
    n: "04",
    title: "Menina deploys",
    body: "The push is matched to your enabled apps, the tag is pinned to a digest, the workload is reconciled, and the route and certificate are published.",
  },
];

const quickstart = `# On the server
curl -fsSL https://menina.moureau.dev/install.sh | sudo sh
sudo menina setup
sudo menina deploy

# Registry push credential
menina registry credential

# From your build machine
docker login registry.example.com -u menina
docker build -t registry.example.com/my-app/api:production .
docker push  registry.example.com/my-app/api:production`;

function Header() {
  return (
    <header class="nav">
      <a class="brand" href="/">
        <span class="brand-logo">Menina</span>
      </a>
      <nav class="nav-links">
        <a href="/llms.txt" target="_blank">
          llms.txt
        </a>
        <a href={GITHUB} target="_blank">
          GitHub
        </a>
        <a class="nav-cta" href={MOUREAU} target="_blank">
          moureau.dev
        </a>
      </nav>
    </header>
  );
}

function Footer() {
  return (
    <footer class="footer">
      <div class="footer-inner">
        <div class="footer-brand">
          <span class="brand-logo">Menina</span>
          <p class="footer-sub">A self-hosted application platform.</p>
        </div>
        <div class="footer-cols">
          <div class="footer-col">
            <h4>Project</h4>
            <a href={GITHUB} target="_blank">
              GitHub
            </a>
            <a href="/llms.txt" target="_blank">
              llms.txt
            </a>
            <a href={`${GITHUB}/tree/main/docs`} target="_blank">
              Documentation
            </a>
            <a href={`${GITHUB}/blob/main/CHANGELOG.md`} target="_blank">
              Changelog
            </a>
          </div>
          <div class="footer-col">
            <h4>Built by</h4>
            <a href={MOUREAU} target="_blank">
              Moureau.dev
            </a>
          </div>
        </div>
      </div>
      <div class="footer-bottom">
        <span>Apache License 2.0</span>
        <span>© 2026 Moureau Development LTDA</span>
      </div>
    </footer>
  );
}

/**
 * @description
 * Menina landing page.
 */
export class Home extends Newstack {
  /* ---------- Lifecycle ---------- */
  prepare({ page }) {
    page.title = "Menina • a self-hosted platform for your containers";
    page.description =
      "Menina turns a VPS into a deployment platform. Push a container image and it runs on k3s, routes public hostnames, and handles HTTPS for you.";
    page.image = "/og.png";
  }

  /* ---------- Render ---------- */
  render() {
    return (
      <div class="page">
        <Header />

        <main>
          <section class="hero">
            <div class="hero-inner">
              <span class="eyebrow">Self-hosted container platform</span>
              <h1>
                Your infrastructure.
                <br />
                Your platform.
              </h1>
              <p class="lede">
                Menina turns a VPS into a deployment platform. Push a container image and it runs on
                k3s, keeps track of releases, and routes public hostnames through its own reverse
                proxy with automatic HTTPS.
              </p>
              <div class="hero-actions">
                <CopyCommand />
              </div>
              <div class="hero-links">
                <a class="btn btn-primary" href={GITHUB} target="_blank">
                  View on GitHub
                </a>
                <a class="btn" href={`${GITHUB}/tree/main/docs`} target="_blank">
                  Documentation
                </a>
                <a class="btn" href="/llms.txt" target="_blank">
                  llms.txt
                </a>
                <a class="btn" href={MOUREAU} target="_blank">
                  moureau.dev
                </a>
              </div>
            </div>
          </section>

          <section id="features" class="section">
            <h2>Nothing to assemble</h2>
            <p class="section-lede">
              A registry, a deploy pipeline, a scheduler, a reverse proxy and TLS. Menina brings
              them together into one system you own.
            </p>
            <div class="grid">
              {features.map((f) => (
                <article class="card">
                  <h3>{f.title}</h3>
                  <p>{f.body}</p>
                </article>
              ))}
            </div>
          </section>

          <section id="how" class="section">
            <h2>How it works</h2>
            <p class="section-lede">From a bare VPS to a running, routable application.</p>
            <ol class="steps">
              {steps.map((s) => (
                <li class="step">
                  <span class="step-n">{s.n}</span>
                  <div>
                    <h3>{s.title}</h3>
                    <p>{s.body}</p>
                  </div>
                </li>
              ))}
            </ol>
          </section>

          <section class="section">
            <h2>Quickstart</h2>
            <pre class="code">{quickstart}</pre>
            <p class="note">
              Configure <code>exposure.hosts</code> for an environment to give an app a route and a
              certificate, then deploy again.
            </p>
          </section>

          <section class="section scope">
            <h2>Scope</h2>
            <p>
              Menina is a small, single-node platform: one SQLite writer, node-local volumes, and a
              single shared push and pull registry credential. It deliberately leaves out HTTP/3,
              ACME wildcards, multi-node high availability, built-in auth and WAF, and canary or
              blue-green rollouts. It stays small enough that you can understand and operate it
              yourself.
            </p>
          </section>
        </main>

        <Footer />
      </div>
    );
  }
}
