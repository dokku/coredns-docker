.PHONY: build
build:
	mkdir -p bin
	rm -rf .coredns-build
	git clone https://github.com/coredns/coredns.git .coredns-build
	cd .coredns-build && git fetch --tags
	cd .coredns-build && git checkout $$(git describe --tags --abbrev=0)
	echo "docker:github.com/dokku/coredns-docker" >> .coredns-build/plugin.cfg
	cd .coredns-build && go mod edit -replace github.com/dokku/coredns-docker=../../coredns-docker
	cd .coredns-build && go mod download
	cd .coredns-build && make gen
	cd .coredns-build && make coredns
	cp .coredns-build/coredns bin/coredns
	# rm -rf .coredns-build

.PHONY: test
test:
	go test -v ./...

.PHONY: clean
clean:
	rm -rf bin
	rm -rf .coredns-build
