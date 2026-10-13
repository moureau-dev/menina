package database

import (
	"context"
)

type Route struct {
	Host         string `json:"host"`
	PathPrefix   string `json:"pathPrefix"`
	Project      string `json:"project"`
	Environment  string `json:"environment"`
	App          string `json:"app"`
	Namespace    string `json:"namespace"`
	Service      string `json:"service"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	RedirectHTTP bool   `json:"redirectHttp"`
	Headers      string `json:"headers"`
	Timeouts     string `json:"timeouts"`
}

func (db *DB) ReplaceRoutes(ctx context.Context, routes []Route) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM routes`); err != nil {
		return err
	}
	for _, r := range routes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO routes
			(host, path_prefix, project, environment, app, namespace, service, port, tls, redirect_http, headers, timeouts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Host, r.PathPrefix, r.Project, r.Environment, r.App, r.Namespace, r.Service, r.Port, boolInt(r.TLS), boolInt(r.RedirectHTTP), r.Headers, r.Timeouts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) Routes(ctx context.Context) ([]Route, error) {
	rows, err := db.QueryContext(ctx, `SELECT host, path_prefix, project, environment, app, namespace, service, port, tls, redirect_http, headers, timeouts
		FROM routes WHERE `+activeWhere+` ORDER BY host, path_prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		var r Route
		var tls, redirect int
		if err := rows.Scan(&r.Host, &r.PathPrefix, &r.Project, &r.Environment, &r.App, &r.Namespace, &r.Service, &r.Port, &tls, &redirect, &r.Headers, &r.Timeouts); err != nil {
			return nil, err
		}
		r.TLS = tls == 1
		r.RedirectHTTP = redirect == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
