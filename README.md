# Consul LB GCE

GCE load balancing based on [Consul](https://www.consul.io/). It is a fork of [@pires's project](https://github.com/pires/consul-lb-gce) which works with network endpoint groups instead of instance groups.

## Set up

### Production
- Setup [Consul](https://www.consul.io/) first and register your services, for example, using [Nomad](https://nomadproject.io/);
- Prepare URL map, instances for your services and `consul-lb-gce`;
- ?(we use HTTP API to manage NEGs) `gcloud` CLI must support [network endpoint groups](https://cloud.google.com/load-balancing/docs/negs);
- Add firewall rule as described [here](https://cloud.google.com/load-balancing/docs/health-checks#fw-rule);
- Build `consul-lb-gce` linux binary executable via `make release`;
- Create `config.json` file near the binary by copying `sample.config.json` and putting your values in;
- Run `consul-lb-gce`, if you want to have logging then use `--stderrthreshold INFO` option.

### Development

`make up` runs application. Although it's difficult to test the app locally, some things can be tested: tracking Consul services, sending requests to GCE, GCE will not be able to work with your network endpoints coz it doesn't aware of them.

`make test` runs tests.

## How it works

The application tracks Consul-registered services with specified in configuration tags. When group of services with tag is found, `consul-lb-gce` tries to create network endpoint group, health check and backend service on GCE, updates provided URL map with new host and path rules, according to these rules requests are navigated to proper backend service which is based on NEG whicn groups service endpoints. When Consul notifies about registering or deleting of a service, `consul-lb-gce` attaches or deattaches endpoints.

Example:

1. Watched tag is single and it is `consullbgce-cdn-ipaffinity-subdomain.domain.com/`;
2. The app creates network endpoint group `neg-consullbgce-cdn-ipaffinity-subdomain-domain-com` if it doesn't exist;
3. The app creates health check `hc-consullbgce-cdn-ipaffinity-subdomain-domain-com` if it doesn't exist;
4. The app creates backend service `bs-consullbgce-cdn-ipaffinity-subdomain-domain-com` based on NEG and HC created above if doesn't exist with enabled CDN and IP affinity;
5. The app configures host rule for `subdomain.domain.com` with path matcher for `/*` which is targeted to created backend service. If path from tag has not only `/` (e.g.: `/path`) then it creates `/path`, `/path/*` path matcher and maps it to created backend service, but `/*` is targeted to default backend service of provided URL map.

## Global/Zonal
- `NEG` - global;
- `BS` - global;
- `HC` - global.

> In future we could support zonal resources.

## Configuration

You can see example [here](./sample.config.json). It has 3 sections which are listed below. 

### Tags

It is an object where key is a tag, value is an object with health check info:

```json
"consullbgce-nocdn:noaffinity:subdomain.domain.com/": {
  "healthCheck": {
    "type": "http",
    "path": "/health"
  }
}
```

Health checks are specified explicitly since Consul doesn't provide them.

**Tag format**

`consullbgce-<cdn|nocdn>:<ipaffinity|noaffinity>:<APPLICATION_ADDRESS>/<PATH>`

Let's take the following initial values:
- We don't need CDN;
- We want to use IP affinity;
- `APPLICATION_ADDRESS` is `api.application.com`;
- `Path` is empty.

So we have the following tag: `consullbgce-nocdn:ipaffinity:api.application.com/`.

### Consul

It is an object with URL to Consul agent or server:

```json
{
  "url": "localhost:8500"
}
```

### Cloud

It is an object with GCE info:

```json
{
  "project": "project-id",
  "network": "network-name", // e.g.: default
  "zone": "region-and-zone", // e.g.: europe-west4-b
  "urlMap": "url-map-name"
}
```
