// The browser half of the passkey ceremonies. The server sends options as
// go-webauthn writes them ({ publicKey: ... } in the JSON forms WebAuthn
// defines), and takes the credential back as its toJSON().

type Options = { publicKey?: unknown };

export async function createPasskey(options: unknown): Promise<unknown> {
  const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(
    (options as Options).publicKey as PublicKeyCredentialCreationOptionsJSON,
  );
  const credential = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  if (!credential) throw new Error("No passkey was created.");
  return credential.toJSON();
}

export async function getPasskey(options: unknown): Promise<unknown> {
  const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(
    (options as Options).publicKey as PublicKeyCredentialRequestOptionsJSON,
  );
  const credential = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!credential) throw new Error("No passkey was chosen.");
  return credential.toJSON();
}

/** passkeyName suggests a name for a passkey made on this device. */
export function passkeyName(): string {
  const ua = navigator.userAgent;
  const device = /iPhone/.test(ua)
    ? "iPhone"
    : /iPad/.test(ua)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /Mac/.test(ua)
          ? "Mac"
          : /Windows/.test(ua)
            ? "Windows"
            : "This device";
  return device;
}
