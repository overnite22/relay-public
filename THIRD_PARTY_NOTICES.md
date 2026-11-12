# Third-party software

Relay is licensed under Apache-2.0. Dependencies retain their upstream licenses.

- Apache Guacamole JavaScript client: Apache-2.0, https://guacamole.apache.org/
- noVNC: MPL-2.0, https://github.com/novnc/noVNC
- React: MIT, https://github.com/facebook/react
- Lucide: ISC, https://github.com/lucide-icons/lucide
- react-markdown / remark-gfm: MIT, https://github.com/remarkjs
- qrcode: MIT, https://github.com/soldair/node-qrcode
- go-chi: MIT, https://github.com/go-chi/chi
- coder/websocket: ISC, https://github.com/coder/websocket
- golang.org/x/crypto: BSD-3-Clause, https://go.googlesource.com/crypto
- modernc.org/sqlite: BSD-3-Clause, https://gitlab.com/cznic/sqlite
- SQLite: public domain, https://www.sqlite.org/copyright.html

The published image contains a dependency license inventory under /licenses.
Build tools and sidecar images retain their upstream licenses.
noVNC is consumed as an unmodified npm dependency; its source is available from
its upstream repository at the version recorded in web/package-lock.json.

The corresponding unmodified noVNC 1.7.0 source is available at
https://github.com/novnc/noVNC/archive/refs/tags/v1.7.0.tar.gz
and https://registry.npmjs.org/@novnc/novnc/-/novnc-1.7.0.tgz .

The guacd sidecar uses Apache Guacamole server 1.6.0 source with the included
PostConnect backport from Apache commit ab36756b596520ae2a94cd4d25e406e0b5aa820b
(GUACAMOLE-2328). Its source checksum and build recipe are in Dockerfile.guacd.
System libraries retain their Alpine package licenses; the package source and
build recipes are published at https://gitlab.alpinelinux.org/alpine/aports
(branch 3.24-stable) and https://pkgs.alpinelinux.org/packages?branch=v3.24 .
