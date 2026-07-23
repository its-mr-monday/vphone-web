import type { ReactNode } from "react";

/**
 * DeviceFrame renders a realistic titanium iPhone shell (rounded bezel, side
 * buttons, and — when the screen is off — a Dynamic Island) around the VM
 * display, so the console reads as a phone rather than a black-bordered rectangle.
 *
 * All chrome geometry is expressed in container-query units (cqw/cqh) resolved
 * against the phone body itself, so the bezel thickness and corner radii stay
 * constant and concentric no matter how the surrounding panel is sized. Corner
 * radii use cqw for both axes, which yields truly circular (not egg-shaped)
 * corners on the tall 1290×2796 aspect.
 *
 * The fake Dynamic Island is shown only while the VM is off: a running guest
 * renders its own island into the video feed, and overlaying a second one just
 * misaligns. children render inside the screen (noVNC canvas + overlays).
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
      <div className="device-outer" style={{ height: `${scale * 100}%` }}>
        <div className="device-body">
          {/* Titanium side buttons */}
          <span className="btn btn-action" />
          <span className="btn btn-volup" />
          <span className="btn btn-voldown" />
          <span className="btn btn-power" />

          <div className="device-screen">
            {children}
            {/* The guest's status bar leaves the center-top clear (clock left,
                battery right), so the island pill sits in that gap — matching a
                real iPhone 15 Pro whether the screen is on or off. */}
            <div className="dynamic-island">
              <span className="di-cam" />
            </div>
            {/* subtle screen glare */}
            <div className="screen-glare" />
          </div>

          {/* soft power indicator */}
          <span className={`power-led ${running ? "on" : ""}`} />
        </div>
      </div>

      <style>{`
        /* The outer element carries the size (via aspect-ratio + height) and acts
           as the container that all cqw/cqh units below resolve against. */
        .device-outer {
          position: relative;
          height: 100%;
          margin: auto;
          flex-shrink: 0;
          aspect-ratio: 1290 / 2796;
          container-type: size;
        }
        .device-body {
          position: absolute;
          inset: 0;
          /* bezel thickness — uniform, relative to phone width */
          padding: 2.5cqw;
          border-radius: 13.3cqw;
          background:
            linear-gradient(145deg, #3a3a3e 0%, #202024 18%, #16161a 50%, #202024 82%, #3a3a3e 100%);
          box-shadow:
            0 0 0 1.5px rgba(255,255,255,0.06),
            0 30px 60px -20px rgba(0,0,0,0.9),
            0 10px 30px -10px rgba(0,0,0,0.7),
            inset 0 0 3px rgba(255,255,255,0.08);
        }
        /* thin inner titanium rail, concentric with the body */
        .device-body::before {
          content: "";
          position: absolute;
          inset: 1cqw;
          border-radius: 12.3cqw;
          background: #0a0a0a;
          box-shadow: inset 0 0 0 1px rgba(255,255,255,0.05);
        }
        /* The screen's aspect ratio is locked to the guest framebuffer
           (1292×2800) so the noVNC canvas fills it exactly — no letterbox strips
           — and native RFB input stays pixel-accurate. It's centered inside the
           body with a thin bezel; matching the aspect makes the top/bottom bezel
           marginally taller than the sides, which reads as a normal phone. */
        .device-screen {
          position: absolute;
          top: 50%;
          left: 50%;
          transform: translate(-50%, -50%);
          height: 96cqh;
          aspect-ratio: 1292 / 2800;
          border-radius: 10.8cqw;
          overflow: hidden;
          background: #000;
          z-index: 1;
          box-shadow: inset 0 0 0 1px rgba(0,0,0,0.9);
        }
        /* Force the noVNC canvas to fill the screen slot exactly. */
        .device-screen canvas {
          width: 100% !important;
          height: 100% !important;
          margin: 0 !important;
        }
        /* iPhone 15 Pro Dynamic Island proportions (≈125×37 pt on a 393 pt wide
           screen): width 29cqw, height 8.6cqw → a correctly proportioned pill. */
        .dynamic-island {
          position: absolute;
          top: 1.2cqh;
          left: 50%;
          transform: translateX(-50%);
          width: 29cqw;
          height: 8.6cqw;
          background: #000;
          border-radius: 999px;
          z-index: 30;
          display: flex;
          align-items: center;
          justify-content: flex-end;
          padding-right: 5cqw;
          pointer-events: none;
        }
        .di-cam {
          width: 2.9cqw;
          height: 2.9cqw;
          border-radius: 50%;
          background: radial-gradient(circle at 35% 35%, #1c2a3a 0%, #05070a 60%, #000 100%);
          box-shadow: inset 0 0 2px rgba(80,140,220,0.35);
        }
        .screen-glare {
          position: absolute;
          inset: 0;
          z-index: 25;
          pointer-events: none;
          background: linear-gradient(135deg, rgba(255,255,255,0.05) 0%, rgba(255,255,255,0) 22%);
        }
        /* side buttons — width relative to phone width, position relative to height */
        .btn {
          position: absolute;
          background: linear-gradient(180deg, #34343a, #17171b);
          border-radius: 2px;
          z-index: 0;
          box-shadow: 0 1px 1px rgba(0,0,0,0.6);
        }
        .btn-action { left: -1cqw; top: 15cqh; width: 1.1cqw; height: 4.2cqh; border-radius: 3px 0 0 3px; }
        .btn-volup  { left: -1cqw; top: 22cqh; width: 1.1cqw; height: 7cqh;   border-radius: 3px 0 0 3px; }
        .btn-voldown{ left: -1cqw; top: 31cqh; width: 1.1cqw; height: 7cqh;   border-radius: 3px 0 0 3px; }
        .btn-power  { right: -1cqw; top: 25cqh; width: 1.1cqw; height: 9cqh;   border-radius: 0 3px 3px 0; }
        .power-led {
          position: absolute;
          bottom: 0.6cqh;
          left: 50%;
          transform: translateX(-50%);
          width: 1.2cqw; height: 1.2cqw; border-radius: 50%;
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
