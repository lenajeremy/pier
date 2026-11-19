# Pier

Pier is a deploy platform for the server you already have: `git push` any
branch and it's built, kept, and served at its own address with HTTPS.

This repository holds the product and its website.

```
pier/     The product. pierd (the daemon) and pier (the CLI), in Go.
site/     The website and waitlist: two landing pages and a small Go server.
```

The design (landing pages, design system, components, patterns and the
dashboard) lives on the "Pier website Claude" Screenshotter board, not in
this repository.

## Commands

Run `make help` for the full list. The common ones:

```sh
make pier-test    # the product's tests
make pier-e2e     # push real projects through Caddy, pierd and Docker
make site         # build the website
make test         # everything
```

See [pier/README.md](pier/README.md) and [site/README.md](site/README.md).
