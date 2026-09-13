# Deploying MACH402

Two servers, one command each.

| Server | Runs | Public address |
|---|---|---|
| **Registry server** | Postgres, the registry, Caddy (HTTPS) | `https://api.example.com` |
| **Web server** | The website, Caddy (HTTPS) | `https://example.com` |

Provider nodes are not deployed here — providers run them on their own GPU
machines with the command from `/provide`. The facilitator and Hedera are
external services.

Caddy obtains and renews Let's Encrypt certificates itself. Nothing else is
published: Postgres and the registry are reachable only inside the registry
server's Docker network, and the website only through its Caddy.

## Before you start

- Two Linux servers with Docker Engine and the Compose plugin
  (`docker compose version`). 1 vCPU / 1 GB is enough for the registry; the web
  server needs **2 GB of RAM** (or swap) to build the site.
- A domain, with DNS **A records** pointing at the servers:
  - `api.example.com` → registry server
  - `example.com` → web server
- Ports **80 and 443** open to the internet on both servers (80 is how Let's
  Encrypt verifies the domain).
- The code on `main`. The provider install command fetches `scripts/install.sh`
  from `main`, so merge before sharing the site.

## 1. Registry server

```sh
git clone https://github.com/YashIIT0909/MACH402.git
cd MACH402/deploy/registry
cp .env.example .env
```

Edit `.env`:

```sh
POSTGRES_PASSWORD=<output of: openssl rand -hex 24>
REGISTRY_DOMAIN=api.example.com
```

Start everything:

```sh
docker compose up -d --build
```

Check:

```sh
docker compose ps                         # postgres and registry "healthy", caddy "running"
curl https://api.example.com/health       # {"ok":true,"online_window_seconds":90}
curl https://api.example.com/v1/nodes     # {"nodes":[],...}
```

## 2. Web server

```sh
git clone https://github.com/YashIIT0909/MACH402.git
cd MACH402/deploy/web
cp .env.example .env
```

Edit `.env`:

```sh
REGISTRY_URL=https://api.example.com
WEB_DOMAIN=example.com
```

Start it (the first build takes a few minutes):

```sh
docker compose up -d --build
```

Check: `https://example.com` loads, and `https://example.com/nodes` shows
"Nothing listed yet" rather than "Could not reach the registry".

## 3. List a node

On a GPU machine, open `https://example.com/provide`, fill it in, and run the
command it gives you. Its `REGISTRY_URL` is already your registry. The node
appears on `/nodes` within seconds of starting.

## Everyday operations

Run these in the matching `deploy/<server>` directory. `make deploy-registry`
and `make deploy-web` from the repo root do the same as `up -d --build`.

```sh
# update to the latest code
git pull && docker compose up -d --build

docker compose logs -f registry     # or: web, caddy, postgres
docker compose restart registry
docker compose down                 # stop; data is kept in Docker volumes

# back up the registry database
docker compose exec -T postgres pg_dump -U cleargate cleargate > registry-$(date +%F).sql
```

`docker compose down -v` deletes the volumes — the database and Caddy's
certificates. Listings rebuild themselves from heartbeats within 30 seconds,
but offline nodes and listing ownership are lost.

## Trying it without a domain

Leave `REGISTRY_DOMAIN` / `WEB_DOMAIN` unset and Caddy serves plain HTTP on
port 80, so `http://<server-ip>` works. Set `HTTP_PORT` if 80 is taken. Use
this only for a first look: see the next section.

## Known limitation: renting needs HTTPS nodes

A renter's browser calls the node it is renting directly. A site served over
`https://` cannot call a node listed as `http://<ip>:8402` — the browser blocks
it as mixed content — so renting from the deployed site works only with nodes
whose `public_url` is HTTPS. Browsing, listing and the provider flow are
unaffected.
