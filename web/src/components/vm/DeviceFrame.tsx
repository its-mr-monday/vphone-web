import type { ReactNode } from "react";

/**
 * DeviceFrame renders a realistic titanium iPhone shell (Dynamic Island, rounded
 * bezel, side buttons) around the VM display — a Corellium-style device chrome so
 * the console reads as a phone rather than a black-bordered rectangle.
 *
 * The screen slot maintains the guest's 1290×2796 aspect ratio and scales to fit
 * the available height. children render inside the screen (noVNC canvas + overlays).
 */
export function DeviceFrame({
  children,
  running,
  scale = 1,
}: {
  children: ReactNode;
  running: boolean;
  scale?: number;
}) {
  return (
    <div className="flex h-full w-full items-center justify-center overflow-auto p-2">
      <div className="device-body" style={{ height: `${scale * 100}%` }}>
        {/* Titanium side buttons */}
        <span className="btn btn-action" />
        <span className="btn btn-volup" />
        <span className="btn btn-voldown" />
        <span className="btn btn-power" />

        <div className="device-screen">
          {children}
          {/* Dynamic Island sits over the top of the screen */}
          <div className="dynamic-island">
            <span className="di-cam" />
          </div>
          {/* subtle screen glare */}
          <div className="screen-glare" />
        </div>

        {/* soft power indicator */}
        <span className={`power-led ${running ? "on" : ""}`} />
      </div>

      <style>{`
        .device-body {
          position: relative;
          height: 100%;
          flex-shrink: 0;
          margin: auto;
          aspect-ratio: 1290 / 2796;
          padding: 2.6%;
          border-radius: 13% / 6%;
          background:
            linear-gradient(145deg, #3a3a3e 0%, #202024 18%, #16161a 50%, #202024 82%, #3a3a3e 100%);
          box-shadow:
            0 0 0 1.5px rgba(255,255,255,0.06),
            0 30px 60px -20px rgba(0,0,0,0.9),
            0 10px 30px -10px rgba(0,0,0,0.7),
            inset 0 0 3px rgba(255,255,255,0.08);
        }
        /* inner titanium rail */
        .device-body::before {
          content: "";
          position: absolute;
          inset: 1.1%;
          border-radius: 12% / 5.6%;
          background: #0a0a0a;
          box-shadow: inset 0 0 0 1px rgba(255,255,255,0.05);
        }
        .device-screen {
          position: relative;
          height: 100%;
          width: 100%;
          border-radius: 9.2% / 4.4%;
          overflow: hidden;
          background: #000;
          z-index: 1;
          box-shadow: inset 0 0 0 1px rgba(0,0,0,0.9);
        }
        .dynamic-island {
          position: absolute;
          top: 1.15%;
          left: 50%;
          transform: translateX(-50%);
          width: 31%;
          height: 2.55%;
          background: #000;
          border-radius: 999px;
          z-index: 30;
          display: flex;
          align-items: center;
          justify-content: flex-end;
          padding-right: 6%;
          pointer-events: none;
        }
        .di-cam {
          width: 26%;
          height: 46%;
          border-radius: 999px;
          background: radial-gradient(circle at 35% 35%, #1c2a3a 0%, #05070a 60%, #000 100%);
          box-shadow: inset 0 0 2px rgba(80,140,220,0.35);
          max-width: 14px;
          max-height: 14px;
        }
        .screen-glare {
          position: absolute;
          inset: 0;
          z-index: 25;
          pointer-events: none;
          background: linear-gradient(135deg, rgba(255,255,255,0.05) 0%, rgba(255,255,255,0) 22%);
        }
        /* side buttons */
        .btn {
          position: absolute;
          background: linear-gradient(180deg, #34343a, #17171b);
          border-radius: 2px;
          z-index: 0;
          box-shadow: 0 1px 1px rgba(0,0,0,0.6);
        }
        .btn-action { left: -1.1%; top: 15%; width: 1.2%; height: 4.2%; border-radius: 3px 0 0 3px; }
        .btn-volup  { left: -1.1%; top: 22%; width: 1.2%; height: 7%; border-radius: 3px 0 0 3px; }
        .btn-voldown{ left: -1.1%; top: 31%; width: 1.2%; height: 7%; border-radius: 3px 0 0 3px; }
        .btn-power  { right: -1.1%; top: 25%; width: 1.2%; height: 9%; border-radius: 0 3px 3px 0; }
        .power-led {
          position: absolute;
          bottom: 1.4%;
          left: 50%;
          transform: translateX(-50%);
          width: 5px; height: 5px; border-radius: 50%;
          background: #333; z-index: 30;
          transition: all 0.4s;
        }
        .power-led.on {
          background: #00e676;
          box-shadow: 0 0 6px #00e676;
        }
      `}</style>
    </div>
  );
}
