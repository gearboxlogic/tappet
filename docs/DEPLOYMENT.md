# Deployment

Tappet needs its JSON configuration and capability package directory at
runtime. Downstream stdio provider commands must also exist in the image or host
environment.

## Docker

```bash
docker run --rm \
  -p 9090:9090 \
  -v "$PWD/config.docker.json:/config/config.json:ro" \
  -v "$PWD/testdata/capabilities:/config/capabilities:ro" \
  ghcr.io/gearboxlogic/tappet:latest
```

The Docker-specific configuration starts a Streamable HTTP server on port
`9090` and uses the absolute package path `/config/capabilities`. The repository
image includes Node.js, `npx`, and `uvx` for configurations that launch those
provider commands.

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
      - ./testdata/capabilities:/config/capabilities:ro
    ports:
      - "9090:9090"
    restart: unless-stopped
```

Use `authTokens` for outward HTTP authentication. That does not replace
provider-side authorization. Keep capability packages read-only at runtime,
and supply provider credentials through `mcpServers` environment or header
configuration rather than embedding them in packages.
