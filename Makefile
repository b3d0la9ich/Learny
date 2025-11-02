run:
	docker compose up --build

stop:
	docker compose down

migrate:
	docker compose run --rm migrator
