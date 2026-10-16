# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

FROM mikefarah/yq:4.54.1@sha256:4b3d9475d65571d28cbb19544d3820ec2945e4c8b2f18279394282b8dc3a592e AS yq
FROM amazon/aws-cli:2.37.12@sha256:ce44b800602264c12298bbf3399055a4cefdb9ed4a098abe564afe26e7071bce AS aws
FROM postgres:18.6-trixie@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336
COPY --from=yq /usr/bin/yq /usr/local/bin/yq
COPY --from=aws /usr/local/aws-cli /usr/local/aws-cli
RUN ln -s /usr/local/aws-cli/v2/current/bin/aws /usr/local/bin/aws \
    && groupadd --gid 65532 nonroot \
    && useradd --no-log-init --uid 65532 --gid 65532 -d /home/nonroot -m nonroot \
    && chown -R 65532:65532 /var/lib/postgresql /var/run/postgresql

USER 65532:65532
