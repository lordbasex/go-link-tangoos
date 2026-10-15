VERSION   := $(shell cat VERSION)
GOPKGS    := ./cmd/... ./internal/... ./pkg/...
NFPM      := go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0
DEB       := dist/tangoos_$(VERSION)_amd64.deb
WEBUI     := internal/webui/dist

VM        ?= 172.17.0.44
# Ajustes propios del laboratorio (VM, SSH_KEY, PVE…) en local.mk, que no se versiona.
-include local.mk
SSH_KEY   ?= $(HOME)/.ssh/id_ed25519
export SSH_KEY VM
export TANGO_SSH_KEY ?= $(SSH_KEY)
SSH_OPTS  := -i $(SSH_KEY) -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR

.PHONY: web tangod tango-agent tango-installer tangoctl dev check deb deploy-vm vm-ssh clean

## web: build the desktop bundle and copy it into the tangod embed dir
web:
	@if [ ! -f web/package.json ]; then echo "web/ not ready yet: keeping the placeholder bundle"; exit 0; fi; \
	set -e; \
	cd web && (if [ -f package-lock.json ]; then npm ci; else npm install; fi) && npm run build -w @tango/desktop; \
	cd ..; \
	rm -rf $(WEBUI) && mkdir -p $(WEBUI) && cp -R web/apps/desktop/dist/. $(WEBUI)/ && touch $(WEBUI)/.keep && \
	go run ./cmd/tango-bundlesign -dir $(WEBUI) -version $(VERSION)

## tangod: static linux/amd64 binary in dist/
tangod:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" -o dist/tangod ./cmd/tangod

## tango-installer: instalador gráfico de la ISO (solo va en la capa installer.squashfs del live)
tango-installer:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" -o dist/tango-installer ./cmd/tango-installer

## tango-agent: static linux/amd64 privileged agent in dist/
tango-agent:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" -o dist/tango-agent ./cmd/tango-agent

## dev: serve the embedded bundle on http://127.0.0.1:8080
dev:
	go run ./cmd/tangod --dev

## tangoctl: tango/1 CLI over WebRTC for this machine (dist/tangoctl)
##   dist/tangoctl -url 'wss://172.17.0.44/signal?v=1' -insecure -user admin rpc storage.disks.list
tangoctl:
	@mkdir -p dist
	go build -trimpath -o dist/tangoctl ./cmd/tangoctl

## check: gofmt, vet, tests
check:
	@out=$$(gofmt -l cmd internal pkg); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet $(GOPKGS)
	go test $(GOPKGS)

## deb: tangoos_<version>_amd64.deb in dist/
deb: tangod tango-agent
	VERSION=$(VERSION) $(NFPM) package --config build/nfpm.yaml --packager deb --target $(DEB)

## deploy-vm: install the .deb on the dev VM and show the service
deploy-vm: deb
	scp $(SSH_OPTS) $(DEB) root@$(VM):/tmp/
	ssh $(SSH_OPTS) root@$(VM) 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends --reinstall /tmp/$(notdir $(DEB)) >/dev/null && systemctl status tangod --no-pager | head -12'

## release-vm: como deploy-vm pero con el escritorio firmado con la clave de releases (la que acepta
##   https://tangoos.go-link.org). La semilla nunca entra al repo: RELEASE_KEY o TANGO_BUNDLE_KEY.
RELEASE_KEY ?= $(HOME)/.config/tangoos/bundle-release-ed25519
.PHONY: release-vm
release-vm:
	@test -f "$(RELEASE_KEY)" || { echo "falta la semilla de releases en $(RELEASE_KEY)"; exit 1; }
	TANGO_BUNDLE_KEY="$(RELEASE_KEY)" $(MAKE) web
	$(MAKE) deploy-vm

## iso: ISO por imagen (instala en ~1 min, sin internet). Se construye en el host Proxmox (Debian) por SSH.
##   make iso                          → variante product
##   make iso ISO_VARIANT=test         → laboratorio: sin preguntas, IP fija, llave SSH de desarrollo
PVE        ?= 172.17.0.2
ISO_VARIANT ?= product
ISO_TEST_IP ?= 172.17.0.46/24
ISO_TEST_GW ?= 172.17.0.1
ISO_TEST_DISK ?= TANGO-SYS
ISO_DIR    := /var/lib/vz/template/iso
ISO_SRC    := /root/tangoos-iso/src
iso: deb tango-installer
	ssh $(SSH_OPTS) root@$(PVE) 'mkdir -p $(ISO_SRC)/image'
	scp $(SSH_OPTS) $(DEB) system/iso/build.sh system/iso/preseed*.cfg root@$(PVE):$(ISO_SRC)/
	scp $(SSH_OPTS) system/iso/image/* dist/tango-installer root@$(PVE):$(ISO_SRC)/image/
	ssh-keygen -y -f $(SSH_KEY) | ssh $(SSH_OPTS) root@$(PVE) 'cat > $(ISO_SRC)/dev_authorized_keys'
	ssh $(SSH_OPTS) root@$(PVE) 'cd $(ISO_SRC)/image && ./build-image.sh --deb ../$(notdir $(DEB)) --variant $(ISO_VARIANT) \
		--out $(ISO_DIR)/tangoos-$(VERSION)-amd64$(if $(filter test,$(ISO_VARIANT)),-test,).iso \
		$(if $(filter test,$(ISO_VARIANT)),--authorized-keys ../dev_authorized_keys --ip $(ISO_TEST_IP) --gw $(ISO_TEST_GW) --disk $(ISO_TEST_DISK),)'

## iso-netinst: ISO netinst + preseed (fase 1; baja Debian de internet, ~7 min)
iso-netinst: deb
	scp $(SSH_OPTS) $(DEB) system/iso/build.sh system/iso/preseed*.cfg root@$(PVE):$(ISO_SRC)/
	ssh $(SSH_OPTS) root@$(PVE) 'cd $(ISO_SRC) && ./build.sh --iso $(ISO_DIR)/debian-13.4.0-amd64-netinst.iso \
		--deb $(notdir $(DEB)) --out $(ISO_DIR)/tangoos-$(VERSION)-amd64-netinst.iso --variant product'

vm-ssh:
	ssh $(SSH_OPTS) root@$(VM)

clean:
	rm -rf dist

## ---- Pruebas E2E contra el NAS de laboratorio (docs/14, tests/README.md) ----
E2E_ENV  = TANGO_HOST=$(VM) TANGO_SSH_KEY=$(SSH_KEY)
E2E_GO   = $(E2E_ENV) go test -tags e2e -count=1 -v ./tests/e2e/
E2E_LOG  = tests/e2e-logs
.PHONY: e2e-api e2e-storage e2e-security e2e-ui e2e-all

$(E2E_LOG):
	mkdir -p $(E2E_LOG)

## e2e-api: sesión, sistema, carpetas/usuarios/SMB, archivos, instantáneas, apps, servicios, M4
e2e-api: $(E2E_LOG)
	set -o pipefail; $(E2E_GO) -timeout 90m -run 'TestSession|TestSystem|TestShares|TestFiles|TestSnapshots|TestApps|TestServices|TestM4|TestUsersDelete|TestTOTP|TestPreviousVersions|TestTimeMachine|TestWSDiscovery|TestUPSFake' 2>&1 | tee $(E2E_LOG)/api.log

## e2e-security: autenticación, roles, validación, socket del agente, cabeceras, secretos en logs
e2e-security: $(E2E_LOG)
	set -o pipefail; TANGO_E2E_USER=admin TANGO_E2E_PASSWORD="$$(ssh $(SSH_OPTS) root@$(VM) cat /var/lib/tango/initial-admin-password)" $(E2E_GO) -timeout 15m -run 'TestSecurity|TestSecureResume' 2>&1 | tee $(E2E_LOG)/security.log

## e2e-storage: matriz de almacenamiento y grupos (DESTRUCTIVA sobre discos libres; E2E_REBOOT=1 reinicia la VM)
e2e-storage: $(E2E_LOG)
	set -o pipefail; TANGO_E2E_DESTRUCTIVE=1 TANGO_E2E_REBOOT=$(E2E_REBOOT) $(E2E_GO) -timeout 180m -run 'TestStorageMatrix|TestStorageReboot|TestPool' 2>&1 | tee $(E2E_LOG)/storage.log

## e2e-ui: Playwright contra el producto real (E2E_DESTRUCTIVE=1 crea y borra un RAID 5 desde el asistente)
e2e-ui:
	cd web/e2e && (test -d node_modules || npm install --no-audit --no-fund) && npx playwright install chromium && \
	TANGO_HOST=$(VM) TANGO_E2E_DESTRUCTIVE=$(E2E_DESTRUCTIVE) \
	TANGO_E2E_ADMIN_PASSWORD="$$(ssh $(SSH_OPTS) root@$(VM) cat /var/lib/tango/initial-admin-password)" npx playwright test

## e2e-connect: TangoConnect con navegador real (portal local + signalhub de desarrollo, nunca producción).
##   Requiere: signalhub local (CONNECT_SIGNAL), TangoConnect habilitado en el NAS hacia ese signal y el portal
##   armado con la clave pública dev (CONNECT_PORTAL). Ver tests/README.md §TangoConnect.
CONNECT_PORTAL ?= http://127.0.0.1:5174
CONNECT_SIGNAL ?= ws://172.17.0.5:8090/ws?v=1
.PHONY: e2e-connect connect-portal-dev
connect-portal-dev:
	cd web/apps/connect && VITE_SIGNAL_URL='$(CONNECT_SIGNAL)' VITE_BUNDLE_PUBKEY="$$(cat ../../../build/keys/dev-ed25519.pub)" npx vite build
e2e-connect: tangoctl
	cd web/e2e && (test -d node_modules || npm install --no-audit --no-fund) && npx playwright install chromium && \
	TANGO_CONNECT_PORTAL=$(CONNECT_PORTAL) TANGO_CTL=$(CURDIR)/tests/tangoctl-admin.sh TANGO_VM_SSH=$(CURDIR)/tests/vm-ssh.sh \
	TANGO_E2E_ADMIN_PASSWORD="$$(ssh $(SSH_OPTS) root@$(VM) cat /var/lib/tango/initial-admin-password)" npx playwright test connect.spec.ts

## e2e-all: todo, en el orden de docs/14 (el reinicio de la VM va último)
e2e-all: check
	$(MAKE) e2e-security
	$(MAKE) e2e-api
	$(MAKE) e2e-ui E2E_DESTRUCTIVE=1
	$(MAKE) e2e-storage E2E_REBOOT=1

# ---- TangoOS Connect: portal https://tangoos.go-link.org (docs/16) ----------------------------
# connect-web-build: capturas WebP + build estático en web/apps/connect/dist (lo publica scripts/deploy-connect-web.sh).
# connect-web: además arma dist/connect-web, el binario Go que sirve el portal (self-hosting).
# connect-web-preview: el build en http://localhost:5174 para revisarlo.
.PHONY: connect-web-build connect-web connect-web-preview
## Producción: el portal verifica con la clave pública de releases (build/keys/release-ed25519.pub, en el repo);
## la semilla privada vive fuera del repo (TANGO_BUNDLE_KEY, ver release-web / deb-release).
BUNDLE_PUB ?= build/keys/release-ed25519.pub
connect-web-build:
	scripts/optimize-connect-img.sh
	cd web && VITE_BUNDLE_PUBKEY="$$(cat ../$(BUNDLE_PUB))" npm --prefix apps/connect run build

connect-web: connect-web-build
	rm -rf cmd/connect-web/dist && mkdir -p cmd/connect-web/dist && cp -R web/apps/connect/dist/. cmd/connect-web/dist/ && touch cmd/connect-web/dist/.keep
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/connect-web ./cmd/connect-web

connect-web-preview: connect-web-build
	cd web/apps/connect && npx vite preview --port 5174 --strictPort

# ---- Repositorio APT: https://tangoos.go-link.org/apt (docs/08 §Instalación por script) --------
# apt-repo: suma $(DEB) al pool de dist/apt y regenera los índices firmados (clave fuera del repo,
#   en ~/.config/tangoos/apt-gnupg). Para una release: TANGO_BUNDLE_KEY=… make web deb apt-repo.
# apt-publish: lo sube al bucket del portal (perfil AWS tangoos) e invalida CloudFront.
APT_REPO ?= dist/apt
.PHONY: apt-repo apt-publish
apt-repo:
	@test -f $(DEB) || { echo "falta $(DEB): ejecutá make deb"; exit 1; }
	go run ./cmd/tango-aptrepo -out $(APT_REPO) $(DEB)
apt-publish:
	AWS_PROFILE=$${AWS_PROFILE:-tangoos} TANGOOS_DISTRIBUTION=$${TANGOOS_DISTRIBUTION:-ESSYABR5TAKTB} scripts/publish-apt.sh $(APT_REPO)
