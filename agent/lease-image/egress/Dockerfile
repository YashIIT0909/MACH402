# The lease egress proxy: the single container that is attached both to a
# lease's internal network and to the outside, and the only path a renter's
# container has off the provider's machine.
#
# Built from source in this repository rather than pulled from a third party.
# A proxy that every byte of a lease's traffic passes through is not somewhere
# to introduce an image nobody here has read.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /cleargate-egress .

# Scratch: no shell, no package manager, nothing to pivot into if the proxy is
# ever compromised. It needs CA roots to dial TLS upstreams and nothing else.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /cleargate-egress /cleargate-egress

EXPOSE 3128
ENTRYPOINT ["/cleargate-egress"]
