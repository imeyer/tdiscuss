# tdiscuss
Discussion board for your tailnet

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
