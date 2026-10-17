# The OCR function ships as a container image because it needs two binaries
# Lambda's managed runtimes do not carry: the Tesseract engine, and poppler's
# pdftoppm to rasterize a PDF before the engine ever sees it.
#
# Debian rather than Amazon Linux 2023: AL2023's repositories have
# poppler-utils but no tesseract, and building the engine from source would
# add minutes to every image build for nothing. Any base image works here,
# because the Go binary speaks the Lambda Runtime API itself.
FROM debian:bookworm-slim

# tesseract-ocr-eng is the language data; without it the engine starts and
# recognizes nothing.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      tesseract-ocr \
      tesseract-ocr-eng \
      poppler-utils \
      ca-certificates \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

COPY bootstrap /bootstrap

ENTRYPOINT ["/bootstrap"]
