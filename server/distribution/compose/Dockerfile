# syntax=docker/dockerfile:1
FROM debian:bookworm-slim AS verified-release

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        aapt apksigner python3 \
    && rm -rf /var/lib/apt/lists/*

ARG FILEHUB_RELEASE_SHA256
RUN --mount=type=bind,from=release,source=/filehub-installer.tar.gz,target=/tmp/filehub-installer.tar.gz \
    printf '%s  %s\n' "$FILEHUB_RELEASE_SHA256" /tmp/filehub-installer.tar.gz | sha256sum --check --strict \
    && tar -xzf /tmp/filehub-installer.tar.gz -C /opt \
        filehub/artifacts/filehub filehub/artifacts/app/app.apk filehub/artifacts/app/version.json \
        filehub/LICENSE filehub/THIRD_PARTY_NOTICES.md

COPY --from=installer /apk_signature.py /tmp/apk_signature.py
RUN python3 /tmp/apk_signature.py /opt/filehub/artifacts/app

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates ffmpeg libcrypt1 nginx python3 rsync tesseract-ocr tesseract-ocr-rus \
    && rm -rf /var/lib/apt/lists/*

ARG FILEHUB_MEDIA_TOOLS=0
RUN case "$FILEHUB_MEDIA_TOOLS" in \
      0) ;; \
      1) apt-get update \
         && apt-get install -y --no-install-recommends \
             libvips-tools libreoffice-calc libreoffice-impress libreoffice-writer poppler-utils \
         && rm -rf /var/lib/apt/lists/* ;; \
      *) echo 'FILEHUB_MEDIA_TOOLS must be 0 or 1' >&2; exit 1 ;; \
    esac

COPY --from=verified-release /opt/filehub /opt/filehub
COPY --from=installer /apk_signature.py /configure.py /launcher.py /mounting.py /passwords.py /setup_draft.py /storage_model.py /opt/filehub/installer/
COPY --from=installer /web/ /opt/filehub/installer/web/
COPY start.py /opt/filehub/start.py

ENV PYTHONDONTWRITEBYTECODE=1
USER 1000:1000
WORKDIR /var/lib/filehub
EXPOSE 8080
ENTRYPOINT ["/usr/bin/python3", "/opt/filehub/start.py"]
