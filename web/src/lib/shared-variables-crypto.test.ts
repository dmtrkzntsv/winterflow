import { describe, expect, it } from "vitest";
import { p256 } from "@noble/curves/nist.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { gcm } from "@noble/ciphers/aes.js";

import { encryptForServers, type EncryptionKey } from "@/lib/shared-variables";

// decrypt is the agent's half (pkg/crypto/ecies.go) in JS: the payload is
// base64(ephemeral point 65 | IV 12 | ciphertext+tag), the AES key is
// SHA-256 of the ECDH shared X coordinate.
function decrypt(b64: string, priv: Uint8Array): string {
  const raw = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  const eph = raw.subarray(0, 65);
  const iv = raw.subarray(65, 77);
  const shared = p256.getSharedSecret(priv, eph, false).subarray(1, 33);
  return new TextDecoder().decode(gcm(sha256(shared), iv).decrypt(raw.subarray(77)));
}

function keyPair(id: string): { key: EncryptionKey; priv: Uint8Array } {
  const priv = p256.utils.randomSecretKey();
  const point = p256.getPublicKey(priv, false);
  return { key: { server_id: id, name: id, public_key: btoa(String.fromCharCode(...point)) }, priv };
}

describe("encryptForServers", () => {
  it("encrypts once per server, each readable only with that server's key", async () => {
    const a = keyPair("a");
    const b = keyPair("b");
    const cts = await encryptForServers("pa$word\nline 2", [a.key, b.key]);
    expect(Object.keys(cts).sort()).toEqual(["a", "b"]);
    expect(cts.a).not.toBe(cts.b);
    expect(decrypt(cts.a, a.priv)).toBe("pa$word\nline 2");
    expect(decrypt(cts.b, b.priv)).toBe("pa$word\nline 2");
    expect(() => decrypt(cts.a, b.priv)).toThrow();
  });

  it("encrypts only for the listed servers (gap fill)", async () => {
    const a = keyPair("a");
    const b = keyPair("b");
    expect(Object.keys(await encryptForServers("x", [a.key, b.key], ["b"]))).toEqual(["b"]);
  });
});
