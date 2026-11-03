CREATE TABLE websocket_frames (
    exchange_name text NOT NULL,
    connection_id text NOT NULL,
    frame_sequence bigint NOT NULL CHECK (frame_sequence > 0),
    message_type smallint NOT NULL,
    received_at_ns bigint NOT NULL CHECK (received_at_ns > 0),
    payload_encoding smallint NOT NULL CHECK (payload_encoding IN (0, 1)),
    payload_uncompressed_bytes integer NOT NULL CHECK (payload_uncompressed_bytes >= 0),
    payload bytea NOT NULL,
    PRIMARY KEY (connection_id, frame_sequence)
);

CREATE INDEX websocket_frames_received_at_brin
    ON websocket_frames USING brin (received_at_ns);
