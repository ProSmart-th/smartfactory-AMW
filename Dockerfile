FROM alpine:latest

WORKDIR /app

# Install certificates and timezone data (Asia/Bangkok)
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Bangkok

# Copy pre-compiled Linux binary and set executable permission
COPY smartfactory /app/smartfactory
RUN chmod +x /app/smartfactory

# Create folders and copy templates, static files, and configuration
RUN mkdir -p /app/static /app/templates
COPY templates /app/templates
COPY static /app/static
COPY config.json /app/config.json

ENV PORT=8080
EXPOSE 8080

CMD ["/app/smartfactory"]

