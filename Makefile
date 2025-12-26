NAME = coredns-docker
EMAIL = coredns-docker@josediazgonzalez.com
MAINTAINER = dokku
MAINTAINER_NAME = Jose Diaz-Gonzalez
REPOSITORY = coredns-docker
HARDWARE = $(shell uname -m)
SYSTEM_NAME  = $(shell uname -s | tr '[:upper:]' '[:lower:]')
BASE_VERSION ?= 0.1.0
IMAGE_NAME ?= $(MAINTAINER)/$(REPOSITORY)
PACKAGECLOUD_REPOSITORY ?= dokku/dokku-betafish

ifeq ($(CI_BRANCH),release)
	VERSION ?= $(BASE_VERSION)
	DOCKER_IMAGE_VERSION = $(VERSION)
else
	VERSION = $(shell echo "${BASE_VERSION}")build+$(shell git rev-parse --short HEAD)
	DOCKER_IMAGE_VERSION = $(shell echo "${BASE_VERSION}")build-$(shell git rev-parse --short HEAD)
endif

version:
	@echo "$(CI_BRANCH)"
	@echo "$(VERSION)"

define PACKAGE_DESCRIPTION
Runs coredns with the docker plugin enabled
endef

export PACKAGE_DESCRIPTION

LIST = build release release-packagecloud validate
targets = $(addsuffix -in-docker, $(LIST))

.env.docker:
	@rm -f .env.docker
	@touch .env.docker
	@echo "CI_BRANCH=$(CI_BRANCH)" >> .env.docker
	@echo "GITHUB_ACCESS_TOKEN=$(GITHUB_ACCESS_TOKEN)" >> .env.docker
	@echo "IMAGE_NAME=$(IMAGE_NAME)" >> .env.docker
	@echo "PACKAGECLOUD_REPOSITORY=$(PACKAGECLOUD_REPOSITORY)" >> .env.docker
	@echo "PACKAGECLOUD_TOKEN=$(PACKAGECLOUD_TOKEN)" >> .env.docker
	@echo "VERSION=$(VERSION)" >> .env.docker

.PHONY: build
build:
	rm -rf build release
	rm -rf .coredns-build
	git clone https://github.com/coredns/coredns.git .coredns-build
	cd .coredns-build && git fetch --tags
	cd .coredns-build && git checkout $$(git describe --tags --abbrev=0)
	echo "docker:github.com/dokku/coredns-docker" >> .coredns-build/plugin.cfg
	cd .coredns-build && go mod edit -replace github.com/dokku/coredns-docker=../../coredns-docker
	cd .coredns-build && go mod download
	cd .coredns-build && make gen
	cd .coredns-build && make -f Makefile.release release
	mv .coredns-build/build/ build/
	mv .coredns-build/release/ release/
	@if [ -d build ]; then \
		for platform_dir in build/*/; do \
			if [ -d "$$platform_dir" ]; then \
				platform=$$(basename "$$platform_dir"); \
				for arch_dir in "$$platform_dir"*/; do \
					if [ -d "$$arch_dir" ] && [ -f "$$arch_dir/coredns" ]; then \
						arch=$$(basename "$$arch_dir"); \
						mv "$$arch_dir/coredns" "$$platform_dir/coredns-docker-$$arch"; \
						rmdir "$$arch_dir" 2>/dev/null || true; \
					fi; \
				done; \
			fi; \
		done; \
	fi
	mv build/windows/amd64/coredns.exe build/windows/coredns-docker-amd64.exe
	rm -rf build/windows/amd64
	@if [ -d release ]; then \
		for file in release/*; do \
			if [ -f "$$file" ]; then \
				newfile=$$(echo "$$file" | sed 's/coredns/coredns-docker/g'); \
				if [ "$$file" != "$$newfile" ]; then \
					mv "$$file" "$$newfile"; \
				fi; \
			fi; \
		done; \
	fi
	# rm -rf .coredns-build

build-docker-image:
	docker build --rm -q -f Dockerfile -t $(IMAGE_NAME):build .

$(targets): %-in-docker: .env.docker
	docker run \
		--env-file .env.docker \
		--pid host \
		--privileged \
		--rm \
		--volume /var/lib/docker:/var/lib/docker \
		--volume /var/run/docker.sock:/var/run/docker.sock:ro \
		--volume ${PWD}:/src/github.com/$(MAINTAINER)/$(REPOSITORY) \
		--workdir /src/github.com/$(MAINTAINER)/$(REPOSITORY) \
		$(IMAGE_NAME):build make -e $(@:-in-docker=)

build/deb/$(NAME)_$(VERSION)_amd64.deb: build/linux/$(NAME)-amd64
	export SOURCE_DATE_EPOCH=$(shell git log -1 --format=%ct) \
		&& mkdir -p build/deb \
		&& fpm \
		--architecture amd64 \
		--category utils \
		--depends net-tools \
		--depends util-linux \
		--description "$$PACKAGE_DESCRIPTION" \
		--input-type dir \
		--license 'MIT License' \
		--maintainer "$(MAINTAINER_NAME) <$(EMAIL)>" \
		--name $(NAME) \
		--output-type deb \
		--package build/deb/$(NAME)_$(VERSION)_amd64.deb \
		--url "https://github.com/$(MAINTAINER)/$(REPOSITORY)" \
		--vendor "" \
		--version $(VERSION) \
		--verbose \
		build/linux/$(NAME)-amd64=/usr/bin/$(NAME) \
		LICENSE=/usr/share/doc/$(NAME)/copyright

build/deb/$(NAME)_$(VERSION)_arm64.deb: build/linux/$(NAME)-arm64
	export SOURCE_DATE_EPOCH=$(shell git log -1 --format=%ct) \
		&& mkdir -p build/deb \
		&& fpm \
		--architecture arm64 \
		--category utils \
		--depends net-tools \
		--depends util-linux \
		--description "$$PACKAGE_DESCRIPTION" \
		--input-type dir \
		--license 'MIT License' \
		--maintainer "$(MAINTAINER_NAME) <$(EMAIL)>" \
		--name $(NAME) \
		--output-type deb \
		--package build/deb/$(NAME)_$(VERSION)_arm64.deb \
		--url "https://github.com/$(MAINTAINER)/$(REPOSITORY)" \
		--vendor "" \
		--version $(VERSION) \
		--verbose \
		build/linux/$(NAME)-arm64=/usr/bin/$(NAME) \
		LICENSE=/usr/share/doc/$(NAME)/copyright

.PHONY: clean
clean:
	rm -rf build release validation .coredns-build

ci-report:
	docker version
	rm -f ~/.gitconfig

bin/gh-release:
	mkdir -p bin
	curl -o bin/gh-release.tgz -sL https://github.com/progrium/gh-release/releases/download/v2.3.3/gh-release_2.3.3_$(SYSTEM_NAME)_$(HARDWARE).tgz
	tar xf bin/gh-release.tgz -C bin
	chmod +x bin/gh-release

bin/gh-release-body:
	mkdir -p bin
	curl -o bin/gh-release-body "https://raw.githubusercontent.com/dokku/gh-release-body/master/gh-release-body"
	chmod +x bin/gh-release-body

release: build bin/gh-release bin/gh-release-body
	cp build/deb/$(NAME)_$(VERSION)_amd64.deb release/$(NAME)_$(VERSION)_amd64.deb
	cp build/deb/$(NAME)_$(VERSION)_arm64.deb release/$(NAME)_$(VERSION)_arm64.deb
	bin/gh-release create $(MAINTAINER)/$(REPOSITORY) $(VERSION) $(shell git rev-parse --abbrev-ref HEAD)
	bin/gh-release-body $(MAINTAINER)/$(REPOSITORY) v$(VERSION)

release-packagecloud:
	@$(MAKE) release-packagecloud-deb

release-packagecloud-deb: build/deb/$(NAME)_$(VERSION)_amd64.deb build/deb/$(NAME)_$(VERSION)_arm64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/ubuntu/jammy   build/deb/$(NAME)_$(VERSION)_amd64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/ubuntu/noble   build/deb/$(NAME)_$(VERSION)_amd64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/bullseye build/deb/$(NAME)_$(VERSION)_amd64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/bookworm build/deb/$(NAME)_$(VERSION)_amd64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/trixie   build/deb/$(NAME)_$(VERSION)_amd64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/ubuntu/jammy    build/deb/$(NAME)_$(VERSION)_arm64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/ubuntu/noble    build/deb/$(NAME)_$(VERSION)_arm64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/bullseye build/deb/$(NAME)_$(VERSION)_arm64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/bookworm build/deb/$(NAME)_$(VERSION)_arm64.deb
	package_cloud push $(PACKAGECLOUD_REPOSITORY)/debian/trixie   build/deb/$(NAME)_$(VERSION)_arm64.deb

validate:
	mkdir -p validation
	lintian build/deb/$(NAME)_$(VERSION)_amd64.deb || true
	lintian build/deb/$(NAME)_$(VERSION)_arm64.deb || true
	dpkg-deb --info build/deb/$(NAME)_$(VERSION)_amd64.deb
	dpkg-deb --info build/deb/$(NAME)_$(VERSION)_arm64.deb
	dpkg -c build/deb/$(NAME)_$(VERSION)_amd64.deb
	dpkg -c build/deb/$(NAME)_$(VERSION)_arm64.deb
	cd validation && ar -x ../build/deb/$(NAME)_$(VERSION)_amd64.deb
	cd validation && ar -x ../build/deb/$(NAME)_$(VERSION)_arm64.deb
	ls -lah build/deb validation
	sha1sum build/deb/$(NAME)_$(VERSION)_amd64.deb
	sha1sum build/deb/$(NAME)_$(VERSION)_arm64.deb
	apt update
	apt install -y net-tools util-linux
	bats test.bats

prebuild:
	git config --global --add safe.directory $(shell pwd)
	git status

.PHONY: test
test:
	go test -v ./...
