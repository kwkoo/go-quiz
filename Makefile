PREFIX=github.com/kwkoo
PACKAGE=go-quiz
BUILDERNAME=$(PACKAGE)-builder
BASE:=$(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
COVERAGEOUTPUT=coverage.out
COVERAGEHTML=coverage.html
IMAGENAME="ghcr.io/kwkoo/$(PACKAGE)"
VERSION="0.3"
ADMINPASSWORD="password"
SESSIONTIMEOUT=300
DOCKER=docker
NAMESPACE=quiz
INGRESSHOST=quiz.apps.kubecluster.com

.PHONY: run build clean test coverage image runcontainer redis importquizzes importquizzesocp helm-install-k8s helm-install-openshift helm-uninstall

run:
	@ADMINPASSWORD=$(ADMINPASSWORD) SESSIONTIMEOUT=$(SESSIONTIMEOUT) go run $(BASE) -docroot $(BASE)/docroot

short-sessions:
	@ADMINPASSWORD=$(ADMINPASSWORD) SESSIONTIMEOUT=30 REAPERINTERVAL=15 go run $(BASE) -docroot $(BASE)/docroot

build:
	@echo "Building..."
	@go build -o $(BASE)/bin/$(PACKAGE)

clean:
	rm -f \
	  $(BASE)/bin/$(PACKAGE) \
	  $(BASE)/$(COVERAGEOUTPUT) \
	  $(BASE)/$(COVERAGEHTML)

test:
	@go clean -testcache
	@go test -v $(BASE)/...

coverage:
	@go test $(PREFIX)/$(PACKAGE)/pkg -cover -coverprofile=$(BASE)/$(COVERAGEOUTPUT)
	@go tool cover -html=$(BASE)/$(COVERAGEOUTPUT) -o $(BASE)/$(COVERAGEHTML)
	open $(BASE)/$(COVERAGEHTML)

image: 
	$(DOCKER) buildx use $(BUILDERNAME) || $(DOCKER) buildx create --name $(BUILDERNAME) --use
	$(DOCKER) buildx build \
	  --push \
	  --platform=linux/amd64,linux/arm64/v8,linux/arm/v7 \
	  --rm \
	  --build-arg PACKAGE=$(PACKAGE) \
	  -t $(IMAGENAME):$(VERSION) \
	  -t $(IMAGENAME):latest \
	  $(BASE)

runcontainer:
	docker run \
	  --rm \
	  -it \
	  --name $(PACKAGE) \
	  -p 8080:8080 \
	  -e TZ=Asia/Singapore \
	  -e ADMINPASSWORD="$(ADMINPASSWORD)" \
	  $(IMAGENAME):$(VERSION)

redis:
	docker run \
	  --rm \
	  -it \
	  --name redis \
	  -p 6379:6379 \
	  redis:5

importquizzes:
	@curl -XPUT -u admin:$(ADMINPASSWORD) -d @$(BASE)/quizzes.json http://localhost:8080/api/quiz/bulk

importquizzesocp:
	@curl -XPUT -u admin:$(ADMINPASSWORD) -d @$(BASE)/quizzes.json https://`oc get route/quiz-go-quiz -o jsonpath='{.spec.host}'`/api/quiz/bulk

importquizzesk8s:
	@curl -XPUT -u admin:$(ADMINPASSWORD) -d @$(BASE)/quizzes.json http://$(INGRESSHOST)/api/quiz/bulk

# The helm chart is stored in /helm. It was packaged with the following:
# helm package .
#
# index.yaml was created by running:
# helm repo index .
#
helm-install-k8s:
	helm upgrade \
	  --install quiz $(BASE)/helm/go-quiz-0.1.0.tgz \
	  --namespace $(NAMESPACE) \
	  --create-namespace \
	  --set openshift=false \
	  --set quiz.adminPassword=$(ADMINPASSWORD) \
	  --set ingress.host=$(INGRESSHOST)

helm-install-openshift:
	helm upgrade \
	  --install quiz $(BASE)/helm/go-quiz-0.1.0.tgz \
	  --namespace $(NAMESPACE) \
	  --create-namespace \
	  --set openshift=true \
	  --set quiz.adminPassword=$(ADMINPASSWORD)

helm-uninstall:
	helm uninstall quiz --namespace $(NAMESPACE)
