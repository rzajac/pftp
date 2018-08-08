FROM bluefoxicy/proftpd

ENV PROFTPD_PASSIVE_PORTS="21100-21110"
ENV PROFTPD_TLS="on"
ENV PROFTPD_AUTH_ORDER="mod_auth_pam.c* mod_auth_unix.c"
ENV PROFTPD_TLS_REQUIRED="off"
ENV PROFTPD_DEFAULT_ROOT="/home/pftp"

VOLUME ["/home/pftp", "/var/log/proftpd"]

COPY ./tls/server.crt /etc/ssl/certs/proftpd.crt
COPY ./tls/server.key /etc/ssl/private/proftpd.key
COPY ./tls/server.csr /etc/ssl/certs/chain.crt

RUN echo "AllowForeignAddress on" >> /etc/proftpd/proftpd.conf
RUN echo 'LogFormat   ltsv "vhost:%v time:%t host:%a user:%u method:%m path:%F status:%s size:%b restime:%T"' >> /etc/proftpd/proftpd.conf
RUN echo 'ExtendedLog /var/log/proftpd/extended.log ALL ltsv' >> /etc/proftpd/proftpd.conf

RUN useradd pftp
RUN echo 'pftp:pftp' | chpasswd

EXPOSE 20 21