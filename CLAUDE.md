# CLAUDE.md — Project Conventions for new-api

@AGENTS.md

## Claude Code

- Follow the shared project instructions imported from `AGENTS.md`.
- Unless the user explicitly asks for Docker, frontend hot reload, or a specific port topology, start the project in local single-port mode: run the Go backend locally and serve the built `web/default/dist` assets from the same backend port. The default port is `3000`; use `PORT=<port>` only when the user asks for another port. See `doc/local-startup.md`.
- For repeated local starts, reuse the same SQLite database unless the user explicitly asks for a fresh database or a different data state. The default local SQLite path is `SQLITE_PATH=/private/tmp/new-api-local-test.db?_busy_timeout=30000`.
