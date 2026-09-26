.PHONY: migrate

migrate:
	goose -dir db/migrations postgres "$(DB_URL)" up

