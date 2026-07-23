-- IPSW kind: 'iphone' (the iOS device firmware) or 'cloudos' (the PCC research
-- stack). A VM build sources both — IPHONE_SOURCE from an iphone IPSW and
-- CLOUDOS_SOURCE from a cloudos IPSW — which differ for newer iOS (e.g. iOS 27
-- pairs iPhone 27.0 with CloudOS 26.4).
ALTER TABLE ipsws ADD COLUMN kind TEXT NOT NULL DEFAULT 'iphone';
