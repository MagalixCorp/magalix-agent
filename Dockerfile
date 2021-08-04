FROM alpine:3.14

RUN apk --update add --no-cache ca-certificates bash

COPY /build/agent /

ENTRYPOINT ["/agent", "--debug"]
