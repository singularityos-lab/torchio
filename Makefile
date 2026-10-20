PREFIX ?= /usr

.PHONY: all build static test vet install clean

all: build

build:
	go build -o torchio ./cmd/torchio
	go build -o torchiod ./cmd/torchiod

static:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o torchio ./cmd/torchio
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o torchiod ./cmd/torchiod

test:
	go test ./...

vet:
	go vet ./...

install: static
	install -Dm755 torchio $(DESTDIR)$(PREFIX)/bin/torchio
	install -Dm755 torchiod $(DESTDIR)$(PREFIX)/bin/torchiod
	install -Dm644 packaging/torchiod.desktop $(DESTDIR)/etc/xdg/autostart/torchiod.desktop
	install -Dm644 data/usb-quirks.conf $(DESTDIR)$(PREFIX)/share/torchio/usb-quirks.conf
	install -Dm644 data/drivers/generic-pcl5.json $(DESTDIR)$(PREFIX)/share/torchio/drivers/generic-pcl5.json

clean:
	rm -f torchio torchiod
