# tdiscuss

Discussion board for your tailnet

## Building

1. Install [bazelisk](https://github.com/bazelbuild/bazelisk)
1. `make` will run tests and build

## Running for development

1. Be a [tailscale](https://tailscale.com) user
1. Have an [auth key](https://login.tailscale.com/admin/settings/keys) created for the last step in this list.
1. Set up a PostgreSQL database version 17+ (see [README.database-setup.md](README.database-setup.md))
1. `psql < sqlc/schema.sql`
2. `DATABASE_URL=<valid dsn> TS_AUTHKEY=<key from step 2> make run-binary`

## Running for production

### Prerequisites

- PostgreSQL 17+ configured per [README.database-setup.md](README.database-setup.md)
- A [Tailscale auth key](https://login.tailscale.com/admin/settings/keys)
- The `tdiscuss` binary (build with `make` or download a release)

<details>
<summary><strong>Linux (systemd)</strong></summary>

```bash
# Create service account
getent group tdiscuss >/dev/null || groupadd -r tdiscuss
getent passwd tdiscuss >/dev/null || useradd -r -g tdiscuss -d /var/lib/tdiscuss -s /sbin/nologin -c "tdiscuss service account" tdiscuss

# Create directories
install -d -m 0750 -o tdiscuss -g tdiscuss /var/lib/tdiscuss

# Install binary
install -D -m 0755 tdiscuss /usr/bin/tdiscuss

# Install config and service
install -D -m 0640 -o root -g tdiscuss contrib/rpm/tdiscuss.sysconfig /etc/sysconfig/tdiscuss
install -D -m 0644 contrib/tdiscuss.service /usr/lib/systemd/system/tdiscuss.service
```

Edit `/etc/sysconfig/tdiscuss`:

```bash
TS_AUTHKEY=tskey-auth-xxx                 # Your Tailscale auth key
TSNET_HOSTNAME=discuss                    # Your tailnet hostname
DATABASE_URL=postgres://tdiscuss@localhost/tdiscuss?sslmode=disable
OPTIONS="-data-location=/var/lib/tdiscuss"
```

Set up `.pgpass` for the tdiscuss user per [README.database-setup.md](README.database-setup.md#authentication-using-pgpass).

Start the service:

```bash
systemctl daemon-reload
systemctl enable --now tdiscuss
```

</details>

<details>
<summary><strong>macOS (launchd)</strong></summary>

```bash
# Create directories
sudo mkdir -p /usr/local/var/lib/tdiscuss /usr/local/var/log
sudo chown $(whoami) /usr/local/var/lib/tdiscuss

# Install binary
sudo cp tdiscuss /usr/local/bin/tdiscuss

# Create launchd plist
cat > ~/Library/LaunchAgents/com.tdiscuss.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.tdiscuss</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/tdiscuss</string>
        <string>-data-location=/usr/local/var/lib/tdiscuss</string>
        <!-- Add -otlp to enable OpenTelemetry export -->
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>TS_AUTHKEY</key>
        <string>tskey-auth-xxx</string>
        <key>TSNET_HOSTNAME</key>
        <string>discuss</string>
        <key>DATABASE_URL</key>
        <string>postgres://tdiscuss@localhost/tdiscuss?sslmode=disable</string>
        <!-- OpenTelemetry Configuration (requires -otlp flag above) -->
        <!-- <key>OTEL_EXPORTER_OTLP_ENDPOINT</key> -->
        <!-- <string>http://localhost:4318</string> -->
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/usr/local/var/log/tdiscuss.log</string>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/tdiscuss.log</string>
</dict>
</plist>
EOF
```

Edit the plist to set your `TS_AUTHKEY` and `DATABASE_URL`. Set up `~/.pgpass` per [README.database-setup.md](README.database-setup.md#authentication-using-pgpass).

For OpenTelemetry export, add `-otlp` to ProgramArguments and uncomment the OTEL environment variables. See `contrib/rpm/tdiscuss.sysconfig` for all available OTLP options.

Start the service:

```bash
launchctl load ~/Library/LaunchAgents/com.tdiscuss.plist
```

To stop: `launchctl unload ~/Library/LaunchAgents/com.tdiscuss.plist`

</details>

## Issues

Issues building or running? General questions? [File an issue](https://github.com/imeyer/tdiscuss/issues/new)!
