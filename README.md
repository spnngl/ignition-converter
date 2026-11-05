# ignition-converter

Mutating admission webhook that converts the Ignition user data in Cluster API
bootstrap Secrets from spec 2.x to spec 3.0.

## Why

Cluster API's kubeadm bootstrap provider (CABPK) with `format: ignition`
renders a Flatcar-style Container Linux Config and converts it to Ignition
spec 2.3. Fedora CoreOS's Ignition only accepts specs 3.0 to 3.6, so the
machine would fail at first boot. The webhook rewrites `data.value` of the
bootstrap Secret (type `cluster.x-k8s.io/secret`, `data.format` = `ignition`)
on CREATE and UPDATE.

Enable Ignition in CABPK first: environment variable
`EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true`, i.e. the controller flag
`--feature-gates=KubeadmBootstrapFormatIgnition=true`.

## Behavior

- `data.format` is not `ignition`: allowed, untouched.
- `ignition.version` is already 3.x: allowed, untouched. This makes the
  webhook idempotent.
- Spec 2.x: converted with
  [ign-converter](https://github.com/coreos/ign-converter) and replaced via a
  JSON patch on `/data/value`.
- Anything else (not JSON, invalid, unsupported version, files outside the
  `root` filesystem, networkd units, ...): denied with the reason.

KubeadmConfig `diskSetup` and `mounts`:

- `diskSetup.filesystems` convert with no spec 3 `path`: Ignition creates
  the filesystem without mounting it, and CABPK's systemd mount unit for
  each `mounts` entry, converted as is, mounts it at boot. Ignition only
  needs `path` to write files there, and nothing may be: files, directories
  and links go on `root` only.
- `diskSetup.partitions` with `layout: true` are denied: CABPK renders each
  as an empty partition, and spec 3 needs a number or a label. Without
  `layout` (a table wipe only), they convert.

A file with `append: true` (only possible from a user's additional Container
Linux Config; CABPK's own template ignores it) becomes `overwrite: false` plus
`append` contents, which keeps the 2.x meaning.

## Flags

| Flag          | Default | Description           |
| ------------- | ------- | --------------------- |
| `--addr`      | `:8443` | HTTPS listen address  |
| `--cert-file` |         | TLS certificate file  |
| `--key-file`  |         | TLS private key file  |
| `--convert`   | false   | Convert the config on stdin to stdout and exit (used by `e2e.sh`) |
| `--version`   | false   | Print the version and exit |

The certificate is reloaded on every handshake, so renewals need no restart.
On SIGTERM it stops accepting connections, answers the reviews in flight and
exits 0.
Endpoints: `POST /mutate` (AdmissionReview v1), `GET /healthz`.

## Deploy

[`deploy.yaml`](deploy.yaml) holds a cert-manager private CA (self-signed
`Issuer`, CA `Certificate`, CA `Issuer`) and the serving `Certificate` it
signs, the `Deployment` (two replicas spread over nodes, a 5 s `preStop`
sleep so a terminating pod serves until the Service drops it; Kubernetes
1.30 or later), its
`PodDisruptionBudget` and `Service`, and the `MutatingWebhookConfiguration`
(`caBundle` injected from the CA certificate through the
`cert-manager.io/inject-ca-from` annotation, so serving certificate renewals
leave it unchanged). When the CA renews, renew the serving certificate.
Replace `<TAG>` (the image `ghcr.io/spnngl/images/ignition-converter` is
built in [spnngl/images](https://github.com/spnngl/images)) and
`capi-system`. Its webhook:

- `objectSelector` on the label `cluster.x-k8s.io/cluster-name` existing,
- rules CREATE and UPDATE on `secrets`, `failurePolicy: Fail`,
- a CEL `matchCondition`
  `object.type == 'cluster.x-k8s.io/secret' && has(object.data) && 'format' in object.data`.
  Secret data values are base64 in the API object, so the condition only checks
  that the key exists; the handler checks the value.

## Build and test

```sh
make check   # golangci-lint, shellcheck, go mod tidy, vet, test, build
```

`cmd/ignition-converter/testdata/` holds output of CABPK's own renderer
(`bootstrap/kubeadm/pkg/ignition`, `kubeadm init` and `kubeadm join`),
including an additional Container Linux Config appending to `/etc/hosts`.
The certificates and keys under `/etc/kubernetes/pki` are placeholders, so no
key material is in git; `e2e.sh` generates them with `openssl`.

`e2e.sh` boots the converted `kubeadm init` config in the Fedora CoreOS test
VM of [spnngl/images](https://github.com/spnngl/images): set `IMAGES_DIR` to
a checkout with its qemu disk image built, and `BUILD_KUBE_CONTEXT`.

## Releases

Merging the release pull request that release-please keeps open on `main`
tags `vX.Y.Z` and publishes the linux amd64 and arm64 binaries, their
`SHA256SUMS` signed with Sigstore, an SPDX SBOM, and build provenance and SBOM
attestations. The release notes show how to verify a download.
