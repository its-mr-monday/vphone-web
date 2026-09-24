package vm

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// pmd3PatchMarker identifies our applied patch so re-application is idempotent.
const pmd3PatchMarker = "vphone-web patch"

// pmd3ResetUnpatched is the exact upstream reset() tail we replace.
const pmd3ResetUnpatched = `            self.device.reset()
        except USBError:
            pass

        self._reinit(ecid=self.ecid)`

// pmd3ResetPatched releases the stale libusb handle before re-enumerating.
const pmd3ResetPatched = `            self.device.reset()
        except USBError:
            pass

        # ` + pmd3PatchMarker + `: release the pre-reset libusb handle before
        # re-enumerating. On macOS the still-open old handle makes the
        # re-enumerated device's string descriptors read back None, so _find()
        # skips the (still-present, ECID-matching) recovery device forever and
        # the restore hangs at 100% CPU. Dispose + a brief settle fixes it.
        try:
            import time as _vpw_time
            import usb.util as _vpw_usb_util

            _vpw_usb_util.dispose_resources(self.device)
            _vpw_time.sleep(1)
        except Exception:
            pass

        self._reinit(ecid=self.ecid)`

// ensurePMD3Patch applies the libusb-reset patch to the pymobiledevice3 install
// inside the vphone-cli venv if it hasn't been applied yet. In v2, the restore
// runs natively (VPhoneRestore) so this patch is typically a no-op. It remains
// for backwards compatibility with older venvs that may still be present. Safe
// to call on every startup: it is idempotent and never fatal.
func ensurePMD3Patch(vphoneCLIDir string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	matches, _ := filepath.Glob(filepath.Join(
		vphoneCLIDir, ".venv", "lib", "python*", "site-packages", "pymobiledevice3", "irecv.py"))
	if len(matches) == 0 {
		return // venv not built yet; nothing to patch
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, pmd3PatchMarker) {
			continue // already patched
		}
		if !strings.Contains(content, pmd3ResetUnpatched) {
			log.Warn("pmd3 reset() not in expected shape; skipping restore patch", "file", path)
			continue
		}
		patched := strings.Replace(content, pmd3ResetUnpatched, pmd3ResetPatched, 1)
		if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
			log.Warn("failed to apply pmd3 restore patch", "file", path, "err", err)
			continue
		}
		log.Info("applied pmd3 libusb-reset patch (restore reliability)", "file", path)
	}
}
