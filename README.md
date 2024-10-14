# tdiscuss
Discussion board for your tailnet

## Why?

Why not? Who doesn't want a discussion board for their tailnet? (sarcasm) .. Honestly, I've often found myself wanting a small/simple/easy-to-deploy discussion board [a la pgBoard](https://github.com/pgBoard/pgBoard). The apps [golink](https://github.com/tailscale/golink) and [tclip](https://github.com/tailscale-dev/tclip) use the tailscale go library to expose the application securely on your tailnet. I thought this was a good fit for what I'm calling `tdiscuss`. That's why. Well, and it's fun!

## Building

1. Install [bazelisk](https://github.com/bazelbuild/bazelisk)
1. `make` will run tests and build

## Running for development

1. Be a [tailscale](https://tailscale.com) user
1. Have an [auth key](https://login.tailscale.com/admin/settings/keys) created for the last step in this list.
1. Set up a PostgreSQL database version 14+
1. `psql < sqlc/schema.sql`
2. `DATABASE_URL=<valid dsn> TS_AUTHKEY=<key from step 2> make run-binary`

## Running for production

Coming soon...

## Issues

Issues building or running? General questions? File an issue!
