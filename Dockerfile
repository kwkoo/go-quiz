FROM --platform=$BUILDPLATFORM docker.io/golang:1.18.4 as builder

ARG PACKAGE

LABEL builder=true

COPY . /go/src/
RUN \
  set -x \
  && \
  cd /go/src/ \
  && \
  GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -a -installsuffix cgo -o /go/bin/${PACKAGE}


FROM scratch

ARG PACKAGE

LABEL \
  maintainer="kin.wai.koo@gmail.com" \
  io.k8s.description="Quiz web application" \
  org.opencontainers.image.description="Quiz web application" \
  io.openshift.expose-services="8080:http" \
  org.opencontainers.image.source="https://github.com/kwkoo/${PACKAGE}" \
  builder=false

COPY --from=builder /go/bin/${PACKAGE} /usr/local/bin/app

# we need to copy the certificates over because we're connecting over SSL
COPY --from=builder /etc/ssl /etc/ssl

EXPOSE 8080
USER 1001
ENTRYPOINT ["/usr/local/bin/app"]
