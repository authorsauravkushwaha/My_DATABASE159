FROM node:22-alpine AS builder
WORKDIR /workspace
COPY package.json package-lock.json ./
COPY apps/dashboard/package.json apps/dashboard/package.json
COPY packages/client/package.json packages/client/package.json
RUN npm ci
COPY apps/dashboard apps/dashboard
COPY packages/client packages/client
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.29-alpine
COPY apps/dashboard/deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY apps/dashboard/deploy/security-headers.conf /etc/nginx/security-headers.conf
COPY --from=builder /workspace/apps/dashboard/dist /usr/share/nginx/html
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/ || exit 1
