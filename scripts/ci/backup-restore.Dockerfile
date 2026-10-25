# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

FROM mikefarah/yq:4.54.1@sha256:4b3d9475d65571d28cbb19544d3820ec2945e4c8b2f18279394282b8dc3a592e AS yq
FROM amazon/aws-cli:2.38.4@sha256:de807aa682dd3f45d6de5f431b93762691c53f1134b318936dda39f99e321244 AS aws
FROM postgres:18.6-trixie@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6
COPY --from=yq /usr/bin/yq /usr/local/bin/yq
COPY --from=aws /usr/local/aws-cli /usr/local/aws-cli
RUN ln -s /usr/local/aws-cli/v2/current/bin/aws /usr/local/bin/aws \
    && groupadd --gid 65532 nonroot \
    && useradd --no-log-init --uid 65532 --gid 65532 -d /home/nonroot -m nonroot \
    && chown -R 65532:65532 /var/lib/postgresql /var/run/postgresql

USER 65532:65532
