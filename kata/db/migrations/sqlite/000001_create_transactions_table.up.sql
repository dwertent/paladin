CREATE TABLE transactions (
  created_at                TIMESTAMP       NOT NULL,
  updated_at                TIMESTAMP       NOT NULL,
  deleted_at                TIMESTAMP,
  id                        UUID            NOT NULL,
  contract                  TEXT            NOT NULL,
  "from"                    TEXT            NOT NULL,
  sequence_id               UUID,
  payload_json              TEXT,
  payload_rlp               TEXT
);
