FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /dashboard .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /dashboard /dashboard
COPY incidents /data/incidents
EXPOSE 8080
ENTRYPOINT ["/dashboard", "-addr", ":8080", "-incidents", "/data/incidents"]
