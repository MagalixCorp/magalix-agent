#!/bin/bash

handler() {
    kill -TERM ${PID}
    echo "pushing coverage report"
    curl -v -X POST --data-binary @coverage.txt ${CODECOV_URL} 
    exit
}

trap handler INT TERM

./agent-test "$@" &

PID=$! 
wait ${PID}
