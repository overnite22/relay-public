import Guacamole from "guacamole-common-js";
import { Film, Loader2, Pause, Play, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { recordingsApi } from "../api";
import { KindIcon } from "../kinds";
import type { Recording } from "../types";
import { Modal } from "./Modal";

function timeLabel(ms: number) {
  const seconds = Math.floor(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function Player({ id }: { id: string }) {
  const host = useRef<HTMLDivElement>(null);
  const player = useRef<any>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [playing, setPlaying] = useState(false);
  const [duration, setDuration] = useState(0);
  const [position, setPosition] = useState(0);

  useEffect(() => {
    const screen = host.current!;
    const controller = new AbortController();
    // La version embarquée de SessionRecording attend un tunnel pour charger les données.
    const tunnel = new Guacamole.Tunnel();
    tunnel.setState(Guacamole.Tunnel.State.CONNECTING);
    tunnel.disconnect = () => controller.abort();
    const recording = new Guacamole.SessionRecording(tunnel);
    player.current = recording;
    const display = recording.getDisplay();
    screen.replaceChildren(display.getElement());
    const fit = () => {
      const width = display.getWidth();
      const height = display.getHeight();
      if (width && height) display.scale(Math.min(1, screen.clientWidth / width, screen.clientHeight / height));
    };
    display.onresize = fit;
    const observer = new ResizeObserver(fit);
    observer.observe(screen);
    recording.onprogress = setDuration;
    recording.onload = () => {
      recording.seek(0);
      setLoading(false);
    };
    recording.onerror = () => {
      setError("L’enregistrement est illisible.");
      setLoading(false);
    };
    recording.onplay = () => setPlaying(true);
    recording.onpause = () => setPlaying(false);
    recording.onseek = setPosition;

    const load = async () => {
      const parser = new Guacamole.Parser();
      parser.oninstruction = tunnel.oninstruction;
      const stream = await recordingsApi.read(id, controller.signal);
      const reader = stream.pipeThrough(new TextDecoderStream()).getReader();
      try {
        while (!controller.signal.aborted) {
          const chunk = await reader.read();
          if (chunk.done) {
            tunnel.setState(Guacamole.Tunnel.State.CLOSED);
            return;
          }
          parser.receive(chunk.value);
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
    };
    load().catch((err: Error) => {
      if (controller.signal.aborted) return;
      setError(err.message);
      setLoading(false);
    });
    return () => {
      controller.abort();
      observer.disconnect();
      recording.onplay = recording.onpause = recording.onseek = null;
      recording.pause();
      recording.abort();
      player.current = null;
      screen.replaceChildren();
    };
  }, [id]);

  return (
    <div className="record-player">
      <div ref={host} className="record-screen" />
      <div className="record-controls">
        {loading ? <><Loader2 size={14} className="spin" /> Chargement…</> : error ? (
          <span className="form-error">{error}</span>
        ) : (
          <>
            <button className="tb" aria-label={playing ? "Pause" : "Lire"} onClick={() => {
              const recording = player.current;
              if (playing) recording.pause();
              else if (position >= duration) recording.seek(0, () => recording.play());
              else recording.play();
            }}>{playing ? <Pause size={15} /> : <Play size={15} />}</button>
            <input type="range" aria-label="Position de lecture" min={0} max={duration} value={position}
              onChange={(event) => {
                const recording = player.current;
                recording.seek(Number(event.target.value), () => setPosition(recording.getPosition()));
              }} />
            <span className="mono">{timeLabel(position)} / {timeLabel(duration)}</span>
          </>
        )}
      </div>
    </div>
  );
}

export function RecordingsDialog({ onClose }: { onClose: () => void }) {
  const [items, setItems] = useState<Recording[] | null>(null);
  const [selected, setSelected] = useState<Recording | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const refresh = async () => {
    try {
      const recordings = await recordingsApi.list();
      setItems(recordings.sort((a, b) => b.at.localeCompare(a.at)));
      setError("");
    } catch (err) {
      setError((err as Error).message);
    }
  };
  useEffect(() => { void refresh(); }, []);
  const remove = async () => {
    if (!selected) return;
    try {
      await recordingsApi.remove(selected.id);
      setItems((current) => current?.filter((item) => item.id !== selected.id) ?? []);
      setSelected(null);
      setError("");
    } catch (err) {
      setError((err as Error).message);
    }
    setDeleting(false);
  };
  return (
    <Modal title="Enregistrements" icon={<Film size={16} />} width={1100} onClose={onClose}>
      <div className="recordings">
        <div className="session-toolbar">
          <button className="tb" onClick={refresh}><RefreshCw size={14} /> Actualiser</button>
          <button className="tb" disabled={!selected} onClick={() => setDeleting(true)}><Trash2 size={14} /> Supprimer</button>
          {deleting && selected && (
            <>
              <span>Supprimer « {selected.name} » ?</span>
              <button className="btn danger small" onClick={remove}>Supprimer</button>
              <button className="tb" onClick={() => setDeleting(false)}>Annuler</button>
            </>
          )}
          {error && <span className="form-error">{error}</span>}
        </div>
        <div className="recording-body">
          <aside className="recording-list" aria-label="Sessions enregistrées">
            {!items && !error && <div className="tree-empty">Chargement…</div>}
            {items?.length === 0 && <div className="tree-empty">Aucun enregistrement</div>}
            {items?.map((item) => (
              <button key={item.id} className={`recording-item ${selected?.id === item.id ? "selected" : ""}`}
                aria-pressed={selected?.id === item.id} onClick={() => { setSelected(item); setDeleting(false); }}>
                <KindIcon kind={item.kind} />
                <span>
                  <strong>{item.name}</strong>
                  <small>{new Date(item.at).toLocaleString("fr-FR")} · {Math.ceil(item.size / 1024)} Ko</small>
                  {item.limited && <small>Limite de taille atteinte</small>}
                </span>
              </button>
            ))}
          </aside>
          {selected ? <Player key={selected.id} id={selected.id} /> : (
            <div className="empty-state"><p>Sélectionnez une session à relire.</p></div>
          )}
        </div>
      </div>
    </Modal>
  );
}
