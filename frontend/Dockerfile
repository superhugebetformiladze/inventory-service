FROM node:22-alpine AS build
WORKDIR /app

# Ставим зависимости строго по lock-файлу: сборка на сервере даст те же версии.
COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
# tsc --noEmit внутри build: ошибка типов роняет сборку образа, а не всплывает в проде
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
