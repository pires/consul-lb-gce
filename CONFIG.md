# Consul LB GCE Configuration

You can find configuration sample in [file](./sample.config.toml).

## Tag format

`<TAG_PREFIX><cdn|nocdn>:<ipaffinity|noaffinity>:APPLICATION_ADDRESS/[PATH]`

Example:

`TAG_PREFIX` is `consullbgce-`. We don't need CDN. We want to use IP affinity. `APPLICATION_ADDRESS` is `api.application.com`. `Path` is empty. So we have the following tag: `consullbgce-nocdn:ipaffinity:api.application.com/`.
