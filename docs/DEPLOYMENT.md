# Deployment

Tappet needs its JSON configuration and generated hierarchy at runtime. Downstream stdio provider commands must also exist in the image or host environment.

## Docker

```bash
docker run --rm \
  -p 9090:9090 \
  -v "$PWD/config.docker.json:/config/config.json:ro" \
  -v "$PWD/testdata/mcp_hierarchy:/config/hierarchy:ro" \
  ghcr.io/gearboxlogic/tappet:latest
```

The Docker-specific configuration starts a Streamable HTTP server on port `9090` and uses the absolute hierarchy path `/config/hierarchy`. The repository image includes Node.js, `npx`, and `uvx` for configurations that launch those provider commands.

Run `make test-container` to build the image without publishing it, start a temporary container, and verify MCP initialization plus the two-tool outward contract. Set `CONTAINER_ENGINE=podman` to use Podman instead of Docker.

### Image publication

The manual Docker publishing workflow publishes only to `ghcr.io/gearboxlogic/tappet`. Milestone 0 intentionally retired the inherited optional backup-registry path because Tappet has no documented or supported secondary registry. Restoring secondary publication requires a separate reviewed change with an immutable action pin and credentials scoped to that registry.

## Docker Compose

```yaml
services:
  tappet:
    image: ghcr.io/gearboxlogic/tappet:latest
    volumes:
      - ./config.docker.json:/config/config.json:ro
      - ./testdata/mcp_hierarchy:/config/hierarchy:ro
    ports:
      - "9090:9090"
    restart: unless-stopped
```

Use `authTokens` for outward HTTP authentication. That does not replace provider-side authorization. Keep hierarchy JSON read-only at runtime, and supply provider credentials through the environment rather than embedding them in hierarchy files.
