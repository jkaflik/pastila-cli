CREATE VIEW data_view AS
SELECT * FROM data
WHERE fingerprint = reinterpretAsUInt32(unhex({fingerprint:String}))
AND hash = reinterpretAsUInt128(unhex({hash:String}))
ORDER BY time LIMIT 1