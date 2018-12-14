FROM alpine:3.6

RUN apk --update add --no-cache ca-certificates bash

COPY /build/agent /

ENTRYPOINT ["/agent"]
