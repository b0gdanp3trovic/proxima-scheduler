FROM alpine:latest
RUN apk --no-cache add curl bash
COPY label.sh /usr/local/bin/label.sh
RUN chmod +x /usr/local/bin/label.sh
CMD ["/usr/local/bin/label.sh"]
