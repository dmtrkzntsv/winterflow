// ECIES (P-256) encryption matching the backend (pkg/crypto/ecies.go).
//
// We generate an ephemeral P-256 keypair, ECDH it against the server's public
// key to get the shared X coordinate, SHA-256 that to an AES-256 key, then
// AES-GCM encrypt. The wire payload is base64 of:
//
//   [ ephemeral public key (65 bytes, 0x04 || X || Y) | IV (12) | ciphertext+tag ]
//
// The agent reverses this with its EC private key.
//
// Two interchangeable implementations produce that exact layout:
//   - Web Crypto (native), used when available;
//   - noble (pure JS), used on insecure contexts — self-hosted installs are
//     typically reached over plain http on a LAN, where browsers expose
//     crypto.getRandomValues but NOT crypto.subtle.
import { p256 } from "@noble/curves/nist.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { gcm } from "@noble/ciphers/aes.js";

function base64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64);
  const bytes = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

function bytesToBase64(bytes: Uint8Array): string {
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

function assemblePayload(
  ephPoint: Uint8Array,
  iv: Uint8Array,
  ciphertext: Uint8Array,
): string {
  const payload = new Uint8Array(ephPoint.length + iv.length + ciphertext.length);
  payload.set(ephPoint, 0);
  payload.set(iv, ephPoint.length);
  payload.set(ciphertext, ephPoint.length + iv.length);
  return bytesToBase64(payload);
}

// encryptSecret encrypts plaintext for the server identified by its base64
// public-key point. Returns the base64 ECIES payload.
export async function encryptSecret(
  plaintext: string,
  serverPublicKeyB64: string,
): Promise<string> {
  if (globalThis.crypto?.subtle) {
    return encryptWithWebCrypto(plaintext, serverPublicKeyB64);
  }
  return encryptWithNoble(plaintext, serverPublicKeyB64);
}

// --- pure-JS path (insecure contexts: http over LAN) ------------------------

function encryptWithNoble(plaintext: string, serverPublicKeyB64: string): string {
  const serverPoint = base64ToBytes(serverPublicKeyB64);

  const ephPriv = p256.utils.randomSecretKey();
  const ephPoint = p256.getPublicKey(ephPriv, false); // 65B uncompressed

  // getSharedSecret returns the uncompressed shared point; the secret is its
  // 32-byte X coordinate (matching Web Crypto's deriveBits).
  const shared = p256.getSharedSecret(ephPriv, serverPoint, false);
  const sharedX = shared.subarray(1, 33);

  const aesKey = sha256(sharedX);
  const iv = crypto.getRandomValues(new Uint8Array(new ArrayBuffer(12)));
  // noble's gcm appends the 16-byte tag to the ciphertext, same as Web Crypto.
  const ciphertext = gcm(aesKey, iv).encrypt(new TextEncoder().encode(plaintext));

  return assemblePayload(ephPoint, iv, ciphertext);
}

// --- Web Crypto path (secure contexts) ---------------------------------------

async function encryptWithWebCrypto(
  plaintext: string,
  serverPublicKeyB64: string,
): Promise<string> {
  const serverPub = await crypto.subtle.importKey(
    "raw",
    base64ToBytes(serverPublicKeyB64),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );

  const ephemeral = await crypto.subtle.generateKey(
    { name: "ECDH", namedCurve: "P-256" },
    true,
    ["deriveBits"],
  );

  // ECDH shared secret = the 32-byte X coordinate of the shared point.
  const sharedX = new Uint8Array(
    await crypto.subtle.deriveBits(
      { name: "ECDH", public: serverPub },
      ephemeral.privateKey,
      256,
    ),
  );

  // AES-256 key = SHA-256(sharedX).
  const keyBytes = await crypto.subtle.digest("SHA-256", sharedX);
  const aesKey = await crypto.subtle.importKey(
    "raw",
    keyBytes,
    { name: "AES-GCM" },
    false,
    ["encrypt"],
  );

  const iv = crypto.getRandomValues(new Uint8Array(new ArrayBuffer(12)));
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv },
      aesKey,
      new TextEncoder().encode(plaintext),
    ),
  );

  const ephPoint = new Uint8Array(
    await crypto.subtle.exportKey("raw", ephemeral.publicKey),
  );

  return assemblePayload(ephPoint, iv, ciphertext);
}
