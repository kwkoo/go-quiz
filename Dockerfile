FROM golang:1.16.6 as builder
ARG PREFIX=github.com/kwkoo
ARG PACKAGE=go-quiz
LABEL builder=true
LABEL org.opencontainers.image.source https://github.com/kwkoo/go-quiz
COPY . /go/src/
RUN \
  set -x \
  && \
  cd /go/src/ \
  && \
  CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /go/bin/${PACKAGE}

FROM scratch
LABEL maintainer="kin.wai.koo@gmail.com"
LABEL builder=false
COPY --from=builder /go/bin/${PACKAGE} /

# we need to copy the certificates over because we're connecting over SSL
COPY --from=builder /etc/ssl /etc/ssl

EXPOSE 8080
USER 1001
ENTRYPOINT ["/go-quiz"]
