export interface DeviceSpec {
  product: string;
  name: string;
  screenWidth: number;
  screenHeight: number;
  pointWidth: number;
  pointHeight: number;
  scale: number;
  category: "iphone" | "ipad" | "appletv" | "other";
}

export const DEVICES: DeviceSpec[] = [
  // iPhone 16 series
  { product: "iPhone17,3", name: "iPhone 16", screenWidth: 1290, screenHeight: 2796, pointWidth: 430, pointHeight: 932, scale: 3, category: "iphone" },
  { product: "iPhone17,4", name: "iPhone 16 Plus", screenWidth: 1290, screenHeight: 2796, pointWidth: 430, pointHeight: 932, scale: 3, category: "iphone" },
  { product: "iPhone17,1", name: "iPhone 16 Pro", screenWidth: 1206, screenHeight: 2622, pointWidth: 402, pointHeight: 874, scale: 3, category: "iphone" },
  { product: "iPhone17,2", name: "iPhone 16 Pro Max", screenWidth: 1320, screenHeight: 2868, pointWidth: 440, pointHeight: 956, scale: 3, category: "iphone" },
  { product: "iPhone17,5", name: "iPhone 16e", screenWidth: 1170, screenHeight: 2532, pointWidth: 390, pointHeight: 844, scale: 3, category: "iphone" },
  // iPhone 17 series
  { product: "iPhone18,3", name: "iPhone 17", screenWidth: 1206, screenHeight: 2622, pointWidth: 402, pointHeight: 874, scale: 3, category: "iphone" },
  { product: "iPhone18,4", name: "iPhone Air", screenWidth: 1260, screenHeight: 2736, pointWidth: 420, pointHeight: 912, scale: 3, category: "iphone" },
  { product: "iPhone18,1", name: "iPhone 17 Pro", screenWidth: 1206, screenHeight: 2622, pointWidth: 402, pointHeight: 874, scale: 3, category: "iphone" },
  { product: "iPhone18,2", name: "iPhone 17 Pro Max", screenWidth: 1320, screenHeight: 2868, pointWidth: 440, pointHeight: 956, scale: 3, category: "iphone" },
  { product: "iPhone18,5", name: "iPhone 17e", screenWidth: 1170, screenHeight: 2532, pointWidth: 390, pointHeight: 844, scale: 3, category: "iphone" },
  // iPhone 18 / Duo
  { product: "iPhone19,4", name: "iPhone Duo", screenWidth: 1398, screenHeight: 2034, pointWidth: 466, pointHeight: 678, scale: 3, category: "iphone" },
  { product: "iPhone19,2", name: "iPhone 18 Pro", screenWidth: 1206, screenHeight: 2622, pointWidth: 402, pointHeight: 874, scale: 3, category: "iphone" },
  // iPads
  { product: "iPad16,1", name: "iPad mini (A17 Pro)", screenWidth: 1488, screenHeight: 2266, pointWidth: 744, pointHeight: 1133, scale: 2, category: "ipad" },
  { product: "iPad15,7", name: "iPad (A16)", screenWidth: 1640, screenHeight: 2360, pointWidth: 820, pointHeight: 1180, scale: 2, category: "ipad" },
  { product: "iPad15,3", name: "iPad Air 11\" (M3)", screenWidth: 1640, screenHeight: 2360, pointWidth: 820, pointHeight: 1180, scale: 2, category: "ipad" },
  { product: "iPad15,5", name: "iPad Air 13\" (M3)", screenWidth: 2048, screenHeight: 2732, pointWidth: 1024, pointHeight: 1366, scale: 2, category: "ipad" },
  { product: "iPad16,3", name: "iPad Pro 11\" (M4)", screenWidth: 1668, screenHeight: 2420, pointWidth: 834, pointHeight: 1210, scale: 2, category: "ipad" },
  { product: "iPad16,5", name: "iPad Pro 13\" (M4)", screenWidth: 2064, screenHeight: 2752, pointWidth: 1032, pointHeight: 1376, scale: 2, category: "ipad" },
  // Apple TV
  { product: "AppleTV5,3", name: "Apple TV HD", screenWidth: 1920, screenHeight: 1080, pointWidth: 1920, pointHeight: 1080, scale: 1, category: "appletv" },
  { product: "AppleTV14,1", name: "Apple TV 4K (3rd gen)", screenWidth: 3840, screenHeight: 2160, pointWidth: 1920, pointHeight: 1080, scale: 2, category: "appletv" },
];

export function findDevice(product: string): DeviceSpec | undefined {
  return DEVICES.find(d => d.product === product);
}

export function deviceName(product: string): string {
  return findDevice(product)?.name ?? product;
}
