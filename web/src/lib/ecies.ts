// ECIES (P-256) encryption matching the backend (pkg/crypto/ecies.go).
//
// We generate an ephemeral P-256 keypair, ECDH it against the server's public
// key to get the shared X coordinate, SHA-256 that to an AES-256 key, then
// AES-GCM encrypt. The wire payload is base64 of:
//
//   [ ephemeral public key (65 bytes, 0x04 || X || Y) | IV (12) | ciphertext+tag ]
//
// The agent reverses this with its EC private key.

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

// importServerPublicKey turns the base64 uncompressed point (0x04||X||Y) from
// the API into a CryptoKey usable for ECDH.
async function importServerPublicKey(pointB64: string): Promise<CryptoKey> {
  const raw = base64ToBytes(pointB64);
  return crypto.subtle.importKey(
    "raw",
    raw,
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
}

// encryptSecret encrypts plaintext for the server identified by its base64
// public-key point. Returns the base64 ECIES payload.
export async function encryptSecret(
  plaintext: string,
  serverPublicKeyB64: string,
): Promise<string> {
  const serverPub = await importServerPublicKey(serverPublicKeyB64);

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

  // Export the ephemeral public key as the uncompressed point.
  const ephPoint = new Uint8Array(
    await crypto.subtle.exportKey("raw", ephemeral.publicKey),
  );

  const payload = new Uint8Array(ephPoint.length + iv.length + ciphertext.length);
  payload.set(ephPoint, 0);
  payload.set(iv, ephPoint.length);
  payload.set(ciphertext, ephPoint.length + iv.length);

  return bytesToBase64(payload);
}
