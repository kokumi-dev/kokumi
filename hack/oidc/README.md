# Local Dex OIDC

Dex runs as a sidecar in the kokumi-server pod. Issuer `http://localhost:5556` is same inside the pod and on your host.

The Kitchen requests `offline_access`, and Dex's tokens live one minute, so a refresh of an SSO session asks Dex again whether the user stays signed in, at most once a minute. Dex keeps its tokens in an `emptyDir`, so restarting the pod ends every SSO session at its next refresh.

## Users

| Email | Password | Group | SA | Access |
|---|---|---|---|---|
| `admin@kokumi.dev` | `password` | `kokumi-admin` | `kokumi-admin` | Full |
| `editor@kokumi.dev` | `password` | `kokumi-editor` | `kokumi-editor` | Write |
| `viewer@kokumi.dev` | `password` | `kokumi-viewer` | `kokumi-viewer` | Read |
| `approver1@kokumi.dev` | `password` | `kokumi-editor`, `release-approvers` | `kokumi-editor` | Write, eligible approver |
| `approver2@kokumi.dev` | `password` | `kokumi-editor`, `release-approvers` | `kokumi-editor` | Write, eligible approver |
