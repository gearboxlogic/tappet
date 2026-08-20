# Deployment

CapScope needs its JSON configuration and generated hierarchy at runtime. Downstream stdio provider commands must also exist in the image or host environment.

## Docker

```bash
docker run --rm \
  -p 8080:8080 \
  -v "$PWD/config.json:/config/config.json:ro" \
  -v "$PWD/testdata/mcp_hierarchy:/app/testdata/mcp_hierarchy:ro" \
  ghcr.io/gearboxlogic/capscope:latest
```

The repository image includes Node.js, `npx`, and `uvx` for configurations that launch those provider commands.

### Image publication

The manual Docker publishing workflow publishes only to `ghcr.io/gearboxlogic/capscope`. Milestone 0 intentionally retired the inherited optional backup-registry path because CapScope has no documented or supported secondary registry. Restoring secondary publication requires a separate reviewed change with an immutable action pin and credentials scoped to that registry.

## Docker Compose

```yaml
services:
  capscope:
    image: ghcr.io/gearboxlogic/capscope:latest
    volumes:
      - ./config.json:/config/config.json:ro
      - ./testdata/mcp_hierarchy:/app/testdata/mcp_hierarchy:ro
    ports:
      - "8080:8080"
    restart: unless-stopped
```

Use `authTokens` for outward HTTP authentication. That does not replace provider-side authorization. Keep hierarchy JSON read-only at runtime, and supply provider credentials through the environment rather than embedding them in hierarchy files.
