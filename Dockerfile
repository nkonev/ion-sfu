FROM golang:1.14.14-stretch

ENV GO111MODULE=on

WORKDIR $GOPATH/src/github.com/nkonev/ion-sfu

COPY go.mod go.sum ./
RUN cd $GOPATH/src/github.com/nkonev/ion-sfu && go mod download

COPY sfu/ $GOPATH/src/github.com/nkonev/ion-sfu/pkg
COPY cmd/ $GOPATH/src/github.com/nkonev/ion-sfu/cmd
COPY config.toml $GOPATH/src/github.com/nkonev/ion-sfu/config.toml
