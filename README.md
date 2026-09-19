<p align="center">
  <img src="assets/brand/svg/lockup.svg" alt="ActiLens" width="420" />
</p>

ActiLens is a self-hosted workstation activity monitoring and analytics platform.

## Features
- active application and window timeline
- active / idle time
- periodic screenshots
- browser activity
- employee administration, roles, devices and session revocation
- organization-controlled monitoring policies
- one-time employee enrollment without sharing employee passwords
- Russian and English UI
- Windows agent with visible tray + autostart
- Go backend + PostgreSQL
- Docker deployment and optional HTTPS with Caddy

## Linux server

```bash
git clone https://github.com/0xDive/actilens.git
cd actilens
./install-linux.sh --install-docker --open-firewall
```

For a fixed LAN address:

```bash
./install-linux.sh \
  --port 8081 \
  --origin http://192.168.0.249:8081 \
  --open-firewall
```

Admin console:

```text
http://192.168.0.249:8081/admin/
```

## Windows client

Custom Windows builds can bake a backend URL with `ACTILENS_BUILD_SERVER_URL`.
Tagged public releases are server-agnostic; the deployment server is selected at
provisioning time.

### Build locally on Windows

```powershell
.\build-windows-client.ps1 -ServerUrl "http://192.168.0.249:8081"
```

The resulting `.exe` and `.msi` files are copied to `dist-windows\`.

### Build in GitHub Actions

Open **Actions → Build Custom Windows Client → Run workflow** and enter the server
URL, for example:

```text
http://192.168.0.249:8081
```

The workflow uploads normalized `ActiLens-x64.msi` / `ActiLens-x64-setup.exe`
artifacts.

### Production releases

Tagged releases are server-agnostic and publish stable assets:

```text
ActiLens-x64.msi
ActiLens-x64-setup.exe
install-windows-agent.ps1
```

The same release can be provisioned against `http://192.168.0.249:8081`, another
LAN server, or an HTTPS deployment. The provisioning script stores the selected
`ACTILENS_BACKEND_URL` for that Windows user; the agent also reads it directly
from `HKCU\\Environment`, so a Windows sign-out is not required.

For Internet/WAN deployments, use an HTTPS URL such as
`https://tracker.example.com` instead of exposing plain HTTP publicly.

CI/release fallback builds use `http://127.0.0.1:8081` only when no runtime
server has been provisioned.

To publish a release without creating a tag locally, open
**Actions → ActiLens Release → Run workflow**, enter a semantic version such as
`v0.2.0`, and run it. The workflow validates the version, publishes the Docker
image, builds the Windows installers and creates the GitHub Release/tag.

## Employee enrollment

In **Admin → Employees**, choose **Install code / Код установки** for an active
member. The server creates a short-lived one-time enrollment code and stores only its
SHA-256 hash.

On a Windows workstation, the normal deployment path is:

```powershell
.\install-windows-agent.ps1 `
  -ServerUrl "http://192.168.0.249:8081" `
  -EnrollmentToken "atl_enroll_..."
```

The script:
- validates the server URL and enrollment code;
- downloads `ActiLens-x64.msi` from the latest GitHub Release (or accepts `-InstallerPath`);
- stores the server URL and one-time code for the current Windows user;
- installs the ordinary visible ActiLens application;
- launches ActiLens to redeem the one-time code.

After successful enrollment the agent deletes the consumed enrollment code from the
Windows user environment. The normal first-run monitoring/permission flow still
applies.

To install a specific release:

```powershell
.\install-windows-agent.ps1 `
  -ServerUrl "http://192.168.0.249:8081" `
  -EnrollmentToken "atl_enroll_..." `
  -ReleaseTag "v0.2.0"
```

To use an MSI already downloaded from GitHub Actions:

```powershell
.\install-windows-agent.ps1 `
  -ServerUrl "http://192.168.0.249:8081" `
  -EnrollmentToken "atl_enroll_..." `
  -InstallerPath ".\ActiLens-x64.msi"
```

## Operations

```bash
./actilensctl.sh status
./actilensctl.sh logs
./actilensctl.sh update
./actilensctl.sh backup
```

## License
MIT.
