# Database Setup for tdiscuss

This guide covers setting up PostgreSQL 17.x+ for use with tdiscuss.

## PostgreSQL Installation

### Fedora/RHEL

```bash
sudo dnf install postgresql17-server postgresql17
sudo postgresql-setup --initdb
sudo systemctl enable --now postgresql
```

### Debian/Ubuntu

```bash
sudo apt install postgresql-17
sudo systemctl enable --now postgresql
```

### macOS (Homebrew)

```bash
brew install postgresql@17
brew services start postgresql@17
```

## Database and User Setup

### Generate a Secure Password

Generate a random password before creating the database user:

```bash
openssl rand -base64 32
```

Or using `/dev/urandom`:

```bash
tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32; echo
```

Save this password in your `.pgpass` file (see [Using .pgpass](#using-pgpass-recommended) below).

### Create Database and User

Connect to PostgreSQL as the superuser:

```bash
sudo -u postgres psql
```

Create the database and user:

```sql
-- Create a dedicated user for tdiscuss
CREATE USER tdiscuss WITH PASSWORD 'paste-generated-password-here';

-- Create the database
CREATE DATABASE tdiscuss OWNER tdiscuss;

-- Grant privileges
GRANT ALL PRIVILEGES ON DATABASE tdiscuss TO tdiscuss;

-- Connect to the new database to set up schema permissions
\c tdiscuss;

-- Grant schema privileges
GRANT ALL ON SCHEMA public TO tdiscuss;
```

## PostgreSQL Server Configuration

Edit `postgresql.conf` (typically at `/var/lib/pgsql/data/postgresql.conf` or `/etc/postgresql/17/main/postgresql.conf`):

```ini
listen_addresses = 'localhost'  # Use '*' for all interfaces
max_connections = 100

# Memory settings (adjust based on available RAM)
shared_buffers = 256MB
effective_cache_size = 1GB
work_mem = 16MB
maintenance_work_mem = 128MB
```

Edit `pg_hba.conf` to allow connections:

```
# TYPE  DATABASE        USER            ADDRESS                 METHOD

# Local connections
local   tdiscuss        tdiscuss                                scram-sha-256

# IPv4 local connections
host    tdiscuss        tdiscuss        127.0.0.1/32            scram-sha-256

# IPv4 network connections (adjust subnet as needed)
host    tdiscuss        tdiscuss        10.0.0.0/8              scram-sha-256
host    tdiscuss        tdiscuss        192.168.0.0/16          scram-sha-256
```

Reload PostgreSQL after configuration changes:

```bash
sudo systemctl reload postgresql
```

### Authentication using .pgpass

Instead of embedding passwords in connection strings or environment variables, use a `.pgpass` file for secure credential storage.

Create `~/.pgpass` (i.e. in the user's homedir) with the format:

```
hostname:port:database:username:password
```

Example:

```bash
echo "localhost:5432:tdiscuss:tdiscuss:your-secure-password-here" >> ~/.pgpass
chmod 600 ~/.pgpass
```

## Apply the Schema

Apply the database schema from the project:

```bash
psql -U tdiscuss -d tdiscuss -f sqlc/schema.sql
```

Or if connecting remotely:

```bash
psql "postgresql://tdiscuss@localhost:5432/tdiscuss" -f sqlc/schema.sql
```

## Connection String

tdiscuss uses the `DATABASE_URL` environment variable for database configuration formatted as a standard PostgreSQL connection URI:

```
postgresql://[user]:[password]@[host]:[port]/[database]?[parameters]
```

### Examples

**Local connection (with .pgpass):**
```bash
export DATABASE_URL="postgresql://tdiscuss@localhost:5432/tdiscuss"
```

## Running tdiscuss

Once the database and `.pgpass` are configured, run tdiscuss:

```bash
export DATABASE_URL="postgresql://tdiscuss@localhost:5432/tdiscuss"
./tdiscuss
```

Or inline:

```bash
DATABASE_URL="postgresql://tdiscuss@localhost:5432/tdiscuss" ./tdiscuss
```

## Backup and Restore

### Backup

```bash
pg_dump -U tdiscuss -d tdiscuss -F c -f tdiscuss_backup.dump
```

### Restore

```bash
pg_restore -U tdiscuss -d tdiscuss -c tdiscuss_backup.dump
```
