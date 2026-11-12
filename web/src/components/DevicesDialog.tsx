import { Copy, Plus, Smartphone, Trash2 } from "lucide-react";
import QRCode from "qrcode";
import { useEffect, useState, type FormEvent } from "react";
import { devicesApi, type Device } from "../api";
import { Modal } from "./Modal";

const fmt = (iso: string | null) => (iso ? new Date(iso).toLocaleString("fr-FR") : "jamais");

/** Appairage de l'application mobile : un jeton par appareil, affiché une seule fois en QR code. */
export function DevicesDialog({ onClose, onToast }: { onClose: () => void; onToast: (m: string) => void }) {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState<{ device: Device; token: string; server: string; qr: string } | null>(null);

  const load = () =>
    devicesApi
      .list()
      .then(setDevices)
      .catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, []);

  const create = async (ev: FormEvent) => {
    ev.preventDefault();
    if (!name.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      const r = await devicesApi.create(name.trim());
      const qr = await QRCode.toDataURL(r.pairing, { margin: 1, width: 220, errorCorrectionLevel: "M" });
      setPairing({ device: r.device, token: r.token, server: r.server, qr });
      setName("");
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (d: Device) => {
    if (!confirm(`Révoquer l'accès de « ${d.name} » ? L'application devra être appairée à nouveau.`)) return;
    try {
      await devicesApi.remove(d.id);
      if (pairing?.device.id === d.id) setPairing(null);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const copy = (text: string, label: string) =>
    navigator.clipboard
      .writeText(text)
      .then(() => onToast(`${label} copié`))
      .catch(() => onToast("Copie impossible"));

  return (
    <Modal title="Appareils (application mobile)" icon={<Smartphone size={16} />} onClose={onClose} width={760} height={560}>
      <form className="devices-add" onSubmit={create}>
        <label className="field">
          <span>Nouvel appareil</span>
          <input placeholder="iPhone de Josselin" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
        </label>
        <button className="btn primary" type="submit" disabled={!name.trim() || busy}>
          <Plus size={14} /> Appairer
        </button>
      </form>
      {error && <div className="form-error">{error}</div>}

      {pairing && (
        <div className="devices-pairing">
          <img src={pairing.qr} alt="QR code d'appairage" width={220} height={220} />
          <div className="devices-pairing-text">
            <strong>Scannez ce QR code avec l'application Relay</strong>
            <p className="muted">
              Il n'est affiché qu'une seule fois : quittez cette fenêtre et il faudra appairer de nouveau l'appareil « {pairing.device.name} ».
            </p>
            <div className="devices-manual">
              <span className="muted">Saisie manuelle</span>
              <code>{pairing.server}</code>
              <code className="devices-token">{pairing.token}</code>
              <div className="devices-actions">
                <button className="btn small" type="button" onClick={() => copy(pairing.server, "Serveur")}>
                  <Copy size={13} /> Serveur
                </button>
                <button className="btn small" type="button" onClick={() => copy(pairing.token, "Jeton")}>
                  <Copy size={13} /> Jeton
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="audit-table devices-table">
        <table>
          <thead>
            <tr>
              <th>Appareil</th>
              <th>Appairé le</th>
              <th>Dernière utilisation</th>
              <th>Adresse</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {devices?.map((d) => (
              <tr key={d.id}>
                <td>{d.name}</td>
                <td>{fmt(d.createdAt)}</td>
                <td>{fmt(d.lastUsedAt)}</td>
                <td>{d.lastIp || "—"}</td>
                <td>
                  <button className="btn small danger" type="button" title="Révoquer" onClick={() => remove(d)}>
                    <Trash2 size={13} /> Révoquer
                  </button>
                </td>
              </tr>
            ))}
            {devices && devices.length === 0 && (
              <tr>
                <td colSpan={5} className="muted">
                  Aucun appareil appairé. Donnez un nom ci-dessus pour obtenir un QR code.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </Modal>
  );
}
