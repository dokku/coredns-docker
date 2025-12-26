.PHONY: build
build:
	mkdir -p bin
	rm -rf .coredns-build
	git clone --depth 1 https://github.com/coredns/coredns.git .coredns-build
	echo "docker:github.com/dokku/coredns-docker" >> .coredns-build/plugin.cfg
	cd .coredns-build && go mod edit -replace github.com/dokku/coredns-docker=../../coredns-docker
	cd .coredns-build && go get github.com/dokku/coredns-docker
	cd .coredns-build && go get go.opentelemetry.io/collector/internal/telemetry/componentattribute@v0.133.0
	cd .coredns-build && go generate
	cd .coredns-build && make
	cp .coredns-build/coredns bin/coredns
	# rm -rf .coredns-build

.PHONY: test
test:
	go test -v ./...

.PHONY: clean
clean:
	rm -rf bin
	rm -rf .coredns-build
