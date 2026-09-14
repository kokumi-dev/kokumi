# Local Dex OIDC

Dex runs as a sidecar in the kokumi-server pod. Issuer `http://localhost:5556` is same inside the pod and on your host.

## Users

| Email | Password | Group | SA | Access |
|---|---|---|---|---|
| `admin@kokumi.dev` | `password` | `kokumi-admin` | `kokumi-admin` | Full |
| `editor@kokumi.dev` | `password` | `kokumi-editor` | `kokumi-editor` | Write |
| `viewer@kokumi.dev` | `password` | `kokumi-viewer` | `kokumi-viewer` | Read |
