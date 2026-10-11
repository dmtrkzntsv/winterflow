-- A hostname becomes unique per server instead of globally: the key moves
-- from (domain) to (server_id, domain). Every existing row satisfies it.

ALTER TABLE app_domains DROP CONSTRAINT app_domains_pkey, ADD PRIMARY KEY (server_id, domain);
