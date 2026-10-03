# StockFlow Web 前端镜像：node 构建 → nginx 托管静态文件并反代 /api
# 由根目录 docker-compose.yml 的 web 服务引用（context=./web）
FROM node:20-alpine AS build
WORKDIR /src
# 国内网络用 npmmirror；海外部署可删此行
ENV npm_config_registry=https://registry.npmmirror.com
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
ENV TZ=Asia/Shanghai
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
