# The official Laser Feet image: just the lf program, nothing else.
#
#   docker run --rm ghcr.io/billionairedev24/laserfeet version
#
# It starts from an empty image (scratch): no shell, no package manager, and
# no operating-system packages that could need security updates. It runs as an
# unprivileged user. The build context is the folder made by scripts/build.sh
# plus the system's CA certificates (so web.fetch can check HTTPS sites).
FROM scratch
ARG TARGETARCH
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY lf-linux-${TARGETARCH} /usr/local/bin/lf
USER 65534:65534
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/lf"]
CMD ["help"]
