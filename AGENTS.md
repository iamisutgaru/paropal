# Agent Rules (Repo-Local)

## Deploy Procedure

When the user says **"deploy"**, do the following:

1. Verify locally:
   - `go test ./...`
   - `go vet ./...`

2. Build a FreeBSD/amd64 binary in this repo:
   - `CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o daemon .`

3. Upload and atomically replace the daemon on the host:
   - Upload: `scp ./daemon paropal:/home/protected/daemon.new`
   - Replace: `ssh paropal 'set -euo pipefail; chmod 0755 /home/protected/daemon.new; mv /home/protected/daemon.new /home/protected/daemon'`

4. Then **tell the user to run** the shutdown endpoint (bearer token required) to trigger a supervised restart:
   - From anywhere (public URL): `curl -fsS -X POST https://858.nfshost.com/shutdown -H "Authorization: Bearer $SHUTDOWN_BEARER_TOKEN"`
