FROM alpine:3.14

RUN apk --update add --no-cache ca-certificates bash curl

COPY agent-test .
RUN chmod +x agent-test

COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

ENTRYPOINT ["./entrypoint.sh"]
