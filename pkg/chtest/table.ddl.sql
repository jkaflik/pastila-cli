CREATE TABLE data
(
    `fingerprint` UInt32 DEFAULT reinterpretAsUInt32(unhex(fingerprint_hex)),
    `hash` UInt128 DEFAULT reinterpretAsUInt128(unhex(hash_hex)),
    `prev_fingerprint` UInt32 DEFAULT reinterpretAsUInt32(unhex(prev_fingerprint_hex)),
    `prev_hash` UInt128 DEFAULT reinterpretAsUInt128(unhex(prev_hash_hex)),
    `content` String,
    `size` UInt32 MATERIALIZED length(content),
    `time` DateTime64(3) MATERIALIZED now64(),
    `query_id` String MATERIALIZED queryID(),
    `fingerprint_hex` String EPHEMERAL '',
    `hash_hex` String EPHEMERAL '',
    `prev_fingerprint_hex` String EPHEMERAL '',
    `prev_hash_hex` String EPHEMERAL '',
    `is_encrypted` UInt8,
    CONSTRAINT length CHECK length(content) < ((10 * 1024) * 1024),
    CONSTRAINT hash_is_correct CHECK sipHash128(content) = reinterpretAsFixedString(hash),
    CONSTRAINT not_uniform_random CHECK (length(content) < 10000) OR (arrayReduce('entropy', extractAll(content, '.')) < 7),
    CONSTRAINT not_constant CHECK (length(content) < 10000) OR (arrayReduce('uniqUpTo(1)', extractAll(content, '.')) > 1)
)
    ENGINE = MergeTree()
PRIMARY KEY (fingerprint, hash)
ORDER BY (fingerprint, hash)
SETTINGS index_granularity = 8192
