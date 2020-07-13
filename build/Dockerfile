FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/global-load-balancer-operator \
    USER_UID=1001 \
    USER_NAME=global-load-balancer-operator

# install operator binary
COPY build/_output/bin/global-load-balancer-operator ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
