package ir.schayan.messenger;

import android.net.Uri;
import android.webkit.JavascriptInterface;

// What the messenger's pages may ask of the phone, as window.DocfindAndroid:
// the app's version, for the "a newer version" banner, and fetching and
// installing that newer version from inside the app (Updater); a message or a
// ringing call as the phone's own notification; keeping a call going in the
// background (CallService); print the page, which a WebView cannot do by
// itself; and save a file a page made in memory (a blob: link), which the
// download manager cannot fetch. Nothing else -- no contacts, no other site's
// data, no file read.
//
// Answered only while one of the messenger's pages is showing: the WebView
// never leaves the server (MainActivity.isOurs), and this checks again, so a
// page that somehow loaded elsewhere could not use any of it.
public class Bridge {
    static volatile String currentUrl = "";
    private final MainActivity activity;

    Bridge(MainActivity a) {
        activity = a;
    }

    private boolean ours() {
        return MainActivity.isOurs(Uri.parse(currentUrl == null ? "" : currentUrl));
    }

    @JavascriptInterface
    public String version() {
        return BuildConfig.VERSION_NAME;
    }

    @JavascriptInterface
    public int versionCode() {
        return BuildConfig.VERSION_CODE;
    }

    // notify and unnotify: the page, open in the background, shows a new
    // message or a ringing call as the phone's own notification, without any
    // push service (they are abroad, and gone when the international link is
    // cut). important: a call (sound and heads-up).
    @JavascriptInterface
    public void notify(String title, String body, String tag, String url, boolean important) {
        if (ours() && !LiveService.connected()) NotifyJob.local(activity, title, body, tag, url, important);
    }

    @JavascriptInterface
    public void unnotify(String tag) {
        NotifyJob.unlocal(activity, tag);
    }

    // callStart and callStop: a messenger call keeps its microphone (and
    // camera) while the person is in another app (CallService).
    @JavascriptInterface
    public void callStart(boolean video) {
        if (ours()) CallService.start(activity, video);
    }

    @JavascriptInterface
    public void callStop() {
        CallService.stop(activity);
    }

    // update fetches the newer app from docfind and hands it to Android's
    // installer; sha256 and size are what /api/app/android announced.
    @JavascriptInterface
    public void update(String sha256, double size) {
        if (ours()) activity.runOnUiThread(() -> Updater.begin(activity, sha256, (long) size));
    }

    @JavascriptInterface
    public void print(String title) {
        if (ours()) activity.runOnUiThread(() -> activity.printPage(title));
    }

    @JavascriptInterface
    public void saveFile(String name, String mime, String base64) {
        if (ours()) activity.saveFile(name, mime, base64);
    }
}
