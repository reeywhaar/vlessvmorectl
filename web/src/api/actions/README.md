# API actions

One file per base resource, one exported function per endpoint.

## Naming

The function name is derived mechanically from the request, so the mapping is
reversible and nobody has to guess:

```
<method><PathSegmentsInPascalCase>
```

A path parameter becomes `By<Param>`.

| function                  | request                              |
| ------------------------- | ------------------------------------ |
| `getUsers`                | `GET /api/users`                     |
| `postUsers`               | `POST /api/users`                    |
| `getUsersById`            | `GET /api/users/:id`                 |
| `patchUsersById`          | `PATCH /api/users/:id`               |
| `deleteUsersById`         | `DELETE /api/users/:id`              |
| `postUsersByIdResetUsage` | `POST /api/users/:id/reset-usage`    |
| `postUsersByIdRotateSub`  | `POST /api/users/:id/rotate-sub`     |
| `getUsersByIdUsage`       | `GET /api/users/:id/usage`           |
| `getUsersByIdLink`        | `GET /api/users/:id/link`            |

## Why functions rather than methods on a class

A bundler can drop an exported function that a chunk never references; static methods
keep the whole class alive as one unit. The login screen has no business carrying the
code that builds a usage-series request.

## Why the return types matter

Each action's return type records whether that endpoint's response is wrapped in
`Reloaded<T>`. vlessvmore is not consistent about that envelope — create, patch, delete
and reset-usage are wrapped; rotate-sub, reload and token creation are not — and its
`API.md` implies that they all are. These signatures are the only reliable statement of
which is which.
