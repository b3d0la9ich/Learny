# Learny

## Запуск

cp .env.example .env
docker compose up -d --build
docker compose run --rm migrator
Открой http://localhost:8080