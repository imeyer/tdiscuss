# tdiscuss
Discussion board for your tailnet

## Why?

Why not? Who doesn't want a discussion board for their tailnet? Honestly, I've often found myself wanting a small/simple/easy-to-deploy discussion board [a la pgBoard](https://github.com/pgBoard/pgBoard). The apps [golink](https://github.com/tailscale/golink) and [tclip](https://github.com/tailscale-dev/tclip) use the tailscale go library to expose the application securely on your tailnet. I thought this was a good fit for what I'm calling `tdiscuss`. That's why. Well, and it's fun!

## Building

Install [bazelisk](https://github.com/bazelbuild/bazelisk), then see what platforms exist to build for: `bazelisk query //...` .. any of the `:tdiscuss-{linux,darwin}-{amd,arm}64` are targets.

1. `bazelisk build --stamp --workspace_status_command="${PWD}/status.sh" //:tdiscuss-linux-amd64`

## Running

1. Be a [tailscale](https://tailscale.com) user
1. Have an [auth key](https://login.tailscale.com/admin/settings/keys) created for the last step in this list.
1. Set up a PostgreSQL database version 14+
1. `psql < sqlc/schema.sql`
2. `DATABASE_URL=<valid dsn> TS_AUTHKEY=<key from step 2>`

Success looks like
```
{
  "time": "2024-07-07T19:49:37.305788-07:00",
  "level": "INFO",
  "msg": "AuthLoop: state is Running; done"
}
```
