-- A SOCKS5 proxy the connection is opened through, as a URL that may carry
-- the proxy's user and password; encrypted with the secret key, as the
-- password is. Empty connects directly.
ALTER TABLE ${TABLE_PREFIX}connections ADD COLUMN proxy_enc TEXT NOT NULL DEFAULT '';
