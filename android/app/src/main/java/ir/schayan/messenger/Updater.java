package ir.schayan.messenger;

import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import android.webkit.CookieManager;
import android.widget.Toast;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;

// Updater: the newer docfind app, fetched and handed to Android's installer
// from inside the app (asked for on 2026-10-14). The APK comes only from
// docfind itself (BASE/app/messenger.apk, with this session's cookie), its
// SHA-256 must be the one docfind announced (/api/app/android), and Android
// itself refuses an update not signed with the installed app's key. Android
// shows its own confirm screen; nothing is installed without the person.
//
// It uses the platform's PackageInstaller sessions, so no support library is
// needed (the app has none). Progress goes to the page through
// window.DocfindAppUpdate.progress(percent | -1 with a message).
final class Updater {
    private static volatile boolean busy;

    private Updater() {
    }

    static void begin(MainActivity a, String sha256, long size) {
        if (busy) return;
        if (sha256 == null || !sha256.matches("[0-9a-fA-F]{64}")) {
            report(a, -1, "اثر انگشت نسخهٔ تازه معلوم نیست.");
            return;
        }
        // Android 8 and later ask once whether this app may install apps.
        if (Build.VERSION.SDK_INT >= 26 && !a.getPackageManager().canRequestPackageInstalls()) {
            a.startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + a.getPackageName())));
            report(a, -1, "اجازهٔ «نصب برنامه‌های ناشناس» را برای پیام‌رسان روشن کنید و دوباره «به‌روزرسانی» را بزنید.");
            return;
        }
        busy = true;
        new Thread(() -> {
            PackageInstaller pi = a.getPackageManager().getPackageInstaller();
            int id = -1;
            try {
                URL url = new URL(MainActivity.BASE + "/app/messenger.apk");
                HttpURLConnection c = (HttpURLConnection) url.openConnection();
                c.setConnectTimeout(15000);
                c.setReadTimeout(60000);
                String cookie = CookieManager.getInstance().getCookie(url.toString());
                if (cookie != null) c.setRequestProperty("Cookie", cookie);
                if (c.getResponseCode() != 200) throw new Exception("HTTP " + c.getResponseCode());
                long total = c.getContentLengthLong() > 0 ? c.getContentLengthLong() : size;

                PackageInstaller.SessionParams p = new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
                p.setAppPackageName(a.getPackageName());
                id = pi.createSession(p);
                MessageDigest md = MessageDigest.getInstance("SHA-256");
                try (PackageInstaller.Session s = pi.openSession(id)) {
                    try (InputStream in = c.getInputStream(); OutputStream out = s.openWrite("messenger.apk", 0, total > 0 ? total : -1)) {
                        byte[] buf = new byte[64 * 1024];
                        long done = 0;
                        int last = -1, n;
                        while ((n = in.read(buf)) > 0) {
                            out.write(buf, 0, n);
                            md.update(buf, 0, n);
                            done += n;
                            int pct = total > 0 ? (int) (done * 100 / total) : 0;
                            if (pct != last) {
                                last = pct;
                                report(a, Math.min(pct, 99), null);
                            }
                        }
                        s.fsync(out);
                    }
                    if (!hex(md.digest()).equalsIgnoreCase(sha256)) {
                        s.abandon();
                        id = -1;
                        throw new Exception("hash");
                    }
                    Intent done = new Intent(a, Result.class);
                    int flags = Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0;
                    s.commit(PendingIntent.getBroadcast(a, id, done, flags | PendingIntent.FLAG_UPDATE_CURRENT).getIntentSender());
                    id = -1;
                }
                report(a, 100, null);
            } catch (Exception e) {
                if (id >= 0) {
                    try { pi.abandonSession(id); } catch (Exception ignored) { }
                }
                report(a, -1, "hash".equals(e.getMessage())
                        ? "فایل دریافت‌شده با نسخهٔ اعلام‌شده یکی نیست؛ نصب نشد."
                        : "دریافت نسخهٔ تازه انجام نشد؛ اتصال را بررسی کنید و دوباره بزنید.");
            } finally {
                busy = false;
            }
        }).start();
    }

    // report tells the page how far it got: 0..100, or -1 and why it stopped.
    private static void report(MainActivity a, int pct, String why) {
        String js = "window.DocfindAppUpdate&&window.DocfindAppUpdate.progress&&window.DocfindAppUpdate.progress(" + pct + ","
                + (why == null ? "null" : quote(why)) + ")";
        a.runOnUiThread(() -> a.web.evaluateJavascript(js, null));
    }

    private static String quote(String s) {
        StringBuilder b = new StringBuilder("\"");
        for (char ch : s.toCharArray()) {
            if (ch == '"' || ch == '\\') b.append('\\');
            b.append(ch);
        }
        return b.append('"').toString();
    }

    private static String hex(byte[] d) {
        StringBuilder b = new StringBuilder();
        for (byte x : d) b.append(String.format("%02x", x));
        return b.toString();
    }

    // Result is Android's answer about the session: show its confirm screen,
    // or say why it did not install.
    public static final class Result extends BroadcastReceiver {
        @Override
        public void onReceive(Context ctx, Intent intent) {
            int status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
            if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
                Intent confirm = intent.getParcelableExtra(Intent.EXTRA_INTENT);
                if (confirm != null) {
                    confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                    ctx.startActivity(confirm);
                }
            } else if (status != PackageInstaller.STATUS_SUCCESS) {
                Toast.makeText(ctx, "نسخهٔ تازه نصب نشد.", Toast.LENGTH_LONG).show();
            }
        }
    }

}
