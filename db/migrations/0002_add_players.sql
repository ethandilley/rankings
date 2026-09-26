-- +goose Up
CREATE TABLE players(
  id BIGSERIAL PRIMARY KEY,
  owner TEXT NOT NULL,
  player_name TEXT NOT NULL,
  position TEXT NOT NULL,
  team TEXT NOT NULL,
  drafted_at INT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE players;
