# Identity, scopes, audit, and bootstrap

Menina identities are server-side profiles. A client never selects its own
identity as an assertion: the host maps an authenticated key to a profile and
injects the actor. Local root access remains administrative.

## Profiles, keys, and enrollment

Identity administration is local to the host. Run it as root or as a member of
the `menina` group, over SSH or on the host itself.

```bash
menina identity profile create alice --scope deploy --scope read
menina identity profile list
menina identity key add <profile-id> --public-key ~/.ssh/id_ed25519.pub --comment laptop
menina identity key list <profile-id>
menina identity key authorized <key-id>
menina identity key revoke <key-id>
menina identity key rotate <key-id> --public-key ~/.ssh/id_ed25519.pub
```

Keys can have a short TTL with `--ttl 1h`. Rotation adds a new key and revokes
the old one in one transaction unless `--keep-old` is used. Revoking one key
does not affect the profile's other keys.

### Enrollment tokens

```bash
menina identity enroll create <profile-id> --ttl 15m
menina identity enroll consume <token> --public-key "ssh-ed25519 AAAA..."
```

Enrollment tokens are shown once, stored as SHA-256 hashes, expire after the
configured TTL, and can be consumed only once. Consumption attaches the public
key to the profile in the same transaction that marks the token used.

### One-paste bootstrap

```bash
menina identity bootstrap admin
```

This creates or reuses a profile with `admin` scope, creates an enrollment
token, and prints the reachable host, host-key fingerprint, and a client
command:

```bash
menina enroll menina_enroll_... --host root@vps --host-key "ssh-ed25519 AAAA..."
```

`menina enroll` stores the context, reads the client public key, sends the token
to the host over SSH stdin, consumes it, and runs `whoami`. The token is never
stored on the client. Bootstrap assumes the client already has SSH access to run
enrollment.

## Scopes

| Scope | Allows |
| --- | --- |
| `admin` | Everything |
| `read` | Status, routes, releases, jobs, config revisions, audit |
| `deploy` | Deploy, rollback, promote, apply, config rollback, job cancel |
| `secrets` | Secret set/list/remove/rotate/verify/references |
| `registry` | Registry credential management |

Scopes can be narrowed to a project, environment, or app:

```text
project:demo:deploy
project:demo/production:deploy
project:demo/production/api:deploy
```

Global scopes apply to every target. Project scopes apply to that project.
Environment scopes apply to that project and environment. App scopes apply only
to that app. The API authorizer and SSH forced-command wrapper use the same
policy.

## Forced commands

`menina ssh-forced` is the hidden forced-command entry point. It reads
`SSH_ORIGINAL_COMMAND`, requires `MENINA_KEY_FINGERPRINT`, resolves the key to an
active profile, checks the command scope, and runs only allowlisted top-level
commands: `app`, `release`, `job`, `secret`, `config`, `status`, `routes`,
`notify`, `registry`, `apply`, `api-ssh`, `version`, `capabilities`, `audit`, and
`whoami`.

To print the authorized_keys line for a key:

```bash
menina identity key authorized <key-id>
```

It looks like:

```text
environment="MENINA_KEY_FINGERPRINT=SHA256:...",command="/usr/local/bin/menina ssh-forced" ssh-ed25519 AAAA... comment
```

OpenSSH only propagates `environment=` from authorized_keys when
`PermitUserEnvironment yes` is enabled. Prefer a dedicated `AuthorizedKeysCommand`
or a scoped account if that setting is not acceptable. Without the fingerprint
environment, `ssh-forced` refuses the command.

The API bridge (`menina api-ssh`) accepts the same key-derived actor over the
local control socket. Actor headers are trusted only for local socket requests,
never from remote HTTP.

## Audit

Audit events are stored in SQLite and captured for API requests and forced
commands. They record actor, key fingerprint, source, action, target,
project/environment/app, status, message, and timestamp.

```bash
menina audit list
menina audit list --actor <profile-id> --project demo --action "POST /deploy" --limit 50
menina audit list --json
```

Denied authorization attempts are recorded. Audit does not store secret values.

## API endpoints

The versioned command envelope is `POST /v1/commands` with `api.Request` /
`api.Response` JSON. Legacy paths remain for compatibility. Identity-aware
transports are:

- Unix control socket with the control token and local actor.
- SSH `api-ssh` bridge with a key-derived actor.
- HTTP listener (`serve --api-listen`) using the control token. Bind it to
  loopback until HTTP identity is added; it currently acts as local admin.

Additional endpoints:

- `POST /config/apply`, `GET /config/revisions`, `POST /config/rollback`
- `GET /audit`

`menina whoami` runs locally on the host; it is not a public endpoint.

## Database migrations

Identity and audit add:

- `0006_identity`: profiles and profile keys.
- `0007_enrollment_tokens`: one-time hashed enrollment tokens.
- `0008_audit`: durable audit events.

These migrations are additive. Existing databases keep their recorded
`schema_migrations` versions.
