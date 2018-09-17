FROM alpine:3.8
RUN apk update -U
RUN apk add go musl-dev gcc make git bzr
COPY . /usr/src/gotalk
WORKDIR /usr/src/gotalk
RUN make deps all
CMD cd bin && ./gotalk
