NAME = agent

VERSION = $(shell printf "%s.%s" \
	$$(git rev-list --count HEAD) \
	$$(git rev-parse --short HEAD) \
)

BRANCH = $(shell git rev-parse --abbrev-ref HEAD)

UPXVERSION := 3.94
UPXDIST := upx-$(UPXVERSION)-amd64_linux.tar.xz
UPX_TMP_DIR := $(shell mktemp -d)

version:
	@echo $(VERSION)

build: build@go

upx:
	# Download UPX and install it
	echo :: $(UPXDIST)
	echo :: $(UPX_TMP_DIR)
	test -e $(UPX_TMP_DIR)/$(UPXDIST) || curl -L -o $(UPX_TMP_DIR)/$(UPXDIST) https://github.com/upx/upx/releases/download/v$(UPXVERSION)/$(UPXDIST)
	tar -C /usr/local/bin -xvf $(UPX_TMP_DIR)/$(UPXDIST)

strip: upx
	#strip build/agent
	#upx --brute build/agent

checkout:
	@go list -f '{{.Dir}}' k8s.io/klog \
		| xargs -n1 -I{} bash -c 'git -C {} checkout -q v0.4.0 || true'

build@go:
	@echo :: building go binary $(VERSION)
	@go get -v -d
	@make checkout
	@rm -rf build/agent
	CGO_ENABLED=0 GOOS=linux go build -o build/agent \
		-ldflags "-X main.version=$(VERSION)" \
		-gcflags "-trimpath $(GOPATH)/src"

image: strip
	@echo :: building image $(NAME):$(VERSION)
	@docker build -t $(NAME):$(VERSION) -f Dockerfile .

anchore_scan: 
	@echo :: scanning image $(NAME):$(VERSION)
	@curl -s https://ci-tools.anchore.io/inline_scan-latest | bash -s -- -f -r "$(NAME):$(VERSION)"

push@%:
	$(eval VERSION ?= latest)
	$(eval TAG ?= $*/$(NAME):$(VERSION))
	@echo :: pushing image $(NAME):$(VERSION)
	@docker tag $(NAME):$(VERSION) $(TAG)
	@docker push $(TAG)

	@if [[ "$(tag-file)" ]]; then echo "$(TAG)" > "$(tag-file)"; fi
	@if [[ "$(version-file)" ]]; then echo "$(VERSION)" > "$(version-file)"; fi

