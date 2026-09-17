import { useCallback, useMemo, useState } from 'react';
import { StatusRail } from './components/StatusRail.jsx';
import { WindowTile } from './components/WindowTile.jsx';
import { ControlRack } from './components/ControlRack.jsx';
import { useSequencerState } from './hooks/useSequencerState.js';
import { useServerClock } from './hooks/useServerClock.js';
import { useTick } from './hooks/useTick.js';
import { api } from './lib/api.js';

/** How often the wall repaints. Playback itself is driven by the clock. */
const TICK_MS = 50;

export default function App() {
  const { state, connection, error, refresh, setError } = useSequencerState();
  const clock = useServerClock();
  const [busy, setBusy] = useState(false);
  useTick(TICK_MS);

  /**
   * Every mutation follows the same path: call the API, then take the state the
   * server sends back. Nothing is guessed locally, so the wall never shows a
   * playlist the backend did not accept.
   */
  const run = useCallback(
    async (operation) => {
      setBusy(true);
      try {
        await operation();
        setError(null);
      } catch (cause) {
        setError(cause.message);
      } finally {
        await refresh();
        setBusy(false);
      }
    },
    [refresh, setError],
  );

  const actions = useMemo(
    () => ({
      startSync: (payload) => run(() => api.startSync(payload)),
      stopSync: () => run(() => api.stopSync()),
      addItem: (windowId, payload) => run(() => api.addItem(windowId, payload)),
      removeItem: (windowId, itemId) => run(() => api.removeItem(windowId, itemId)),
      restartCycle: (windowId) => run(() => api.restartCycle(windowId)),
      createWindow: (name) => run(() => api.createWindow(name)),
      deleteWindow: (windowId) => run(() => api.deleteWindow(windowId)),
      addMedia: (payload) => run(() => api.addMedia(payload)),
      reset: () => run(() => api.resetSeed()),
    }),
    [run],
  );

  const mediaById = useMemo(() => new Map((state?.media ?? []).map((media) => [media.id, media])), [state?.media]);

  if (!state) {
    return (
      <main className="boot">
        <p className="boot-title">{error ? 'Cannot reach the backend' : 'Loading the wall'}</p>
        {error && <p className="boot-detail">{error}</p>}
        {error && (
          <button type="button" className="btn" onClick={refresh}>
            Try again
          </button>
        )}
      </main>
    );
  }

  const nowMs = clock.serverNow();

  return (
    <div className="app">
      <StatusRail
        state={state}
        clock={clock}
        connection={connection}
        nowMs={nowMs}
        onStopSync={actions.stopSync}
        busy={busy}
      />

      {error && (
        <p className="banner" role="alert">
          {error}
        </p>
      )}

      <main className="stage">
        <section className="wall" aria-label="Display windows">
          {state.windows.map((win) => (
            <WindowTile key={win.id} win={win} mediaById={mediaById} sync={state.sync} nowMs={nowMs} />
          ))}
          {state.windows.length === 0 && <p className="wall-empty">No windows yet. Add one from the controls.</p>}
        </section>

        <ControlRack state={state} actions={actions} busy={busy} />
      </main>
    </div>
  );
}
