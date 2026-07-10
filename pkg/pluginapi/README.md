# Vendor Plugin SDK

`pkg/pluginapi` is the public, self-contained contract implemented by statically compiled vendor plugins.

## Boundaries

A plugin may:

- detect vendor/model/version through an injected CLI session;
- declare device-specific capabilities;
- build immutable command plans;
- parse execution transcripts into normalized results.

A plugin may not create SSH sessions, read credentials, acquire locks, schedule tasks, write audits, or access PostgreSQL. Those responsibilities remain in the core service.

## Versioning

Plugins declare both their own version and the SDK version they require. The runtime accepts the same SDK major version when the required version is not newer than the runtime. A major SDK change is breaking.

## Loading

V1 plugins are normal Go packages compiled into the service binary and registered during startup. Dynamic `.so` loading, runtime installation, and hot reload are intentionally unsupported.

`internal/pluginregistry` is the only layer that converts public SDK plans/results into internal domain types.
