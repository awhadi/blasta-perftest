# Choosing a database

blasta keeps accounts, run history and settings in one database. The default is
SQLite (a file on the `blasta-data` volume): nothing to set up. To use **PostgreSQL**
or **MariaDB** with Docker you only edit `.env`; the compose files do the rest.

## PostgreSQL or MariaDB in Docker

1. Copy the template if you have not yet: `cp .env.example .env`
2. In `.env`, enable **one** `COMPOSE_FILE` line:

   ```
   # PostgreSQL
   COMPOSE_FILE=docker-compose.yml:docker-compose.postgres.yml
   # MariaDB
   COMPOSE_FILE=docker-compose.yml:docker-compose.mariadb.yml
   ```

3. Set a database password (letters and digits only, so it is safe inside the
   connection URL):

   ```
   BLASTA_DB_PASSWORD=<output of: openssl rand -hex 24>
   ```

4. Start it:

   ```bash
   docker compose up -d --build
   ```

A `db` container is started next to blasta, with its data in the `blasta-db` volume.
blasta waits until the database is healthy, creates the tables on first start and
upgrades them on later ones. The log shows `database ready driver=postgres` (or
`mysql`). The database is not published on any host port.

## An existing or managed database

Skip the `COMPOSE_FILE` line and set the connection URL in `.env` instead:

| Value | Database |
|---|---|
| `postgres://user:pass@host:5432/blasta?sslmode=require` | PostgreSQL |
| `mysql://user:pass@host:3306/blasta` (or `mariadb://`) | MariaDB / MySQL (`?tls=true` for TLS) |

Create the empty database and user first. URL-encode special characters in the password.

## Good to know

- **No automatic copy from SQLite.** A new database starts empty and the first account
  you create becomes the administrator. To keep existing data, stay on SQLite.
- **The encryption key stays separate.** Stored secrets (SSO, SMTP) are encrypted with
  `secret.key` in the `blasta-data` volume, or with `BLASTA_SECRET_KEY` if you set it.
  Back it up together with the database; without it the stored secrets cannot be read.
- **Backups.** PostgreSQL: `docker compose exec db pg_dump -U blasta blasta > blasta.sql`.
  MariaDB: `docker compose exec db mariadb-dump -ublasta -p"$BLASTA_DB_PASSWORD" blasta > blasta.sql`.
- **Switching back** to SQLite: remove the `COMPOSE_FILE` line and run
  `docker compose up -d`. The `blasta-db` volume is kept until you delete it.
