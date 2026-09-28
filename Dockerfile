FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/spoty_lls ./cmd/spoty_lls \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/get_token ./cmd/get_token

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/spoty_lls /spoty_lls
COPY --from=build /out/get_token /get_token

ENTRYPOINT ["/spoty_lls"]
