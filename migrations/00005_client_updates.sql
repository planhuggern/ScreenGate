-- +goose Up
CREATE TABLE client_update_key (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    private_key BLOB NOT NULL
);

-- +goose Down
DROP TABLE client_update_key;
