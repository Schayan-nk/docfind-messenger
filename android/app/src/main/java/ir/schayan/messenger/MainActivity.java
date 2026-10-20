package ir.schayan.messenger;

import android.Manifest;
import android.app.Activity;
import android.app.DownloadManager;
import android.content.ContentValues;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.CancellationSignal;
import android.os.Environment;
import android.os.ParcelFileDescriptor;
import android.print.PageRange;
import android.print.PrintAttributes;
import android.print.PrintDocumentAdapter;
import android.print.PrintManager;
import android.provider.MediaStore;
import android.util.Base64;
import android.view.View;
import android.webkit.CookieManager;
import android.webkit.GeolocationPermissions;
import android.webkit.PermissionRequest;
import android.webkit.URLUtil;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;

// The messenger in a WebView: every page and feature the web application has,
// the same session, the same permissions -- this app adds nothing to what a
// person may see. What it adds is what a browser tab cannot do: calls and
// messages that arrive with the app closed (LiveService, NotifyJob), a call
// that keeps going in another app (CallService); and what a WebView leaves out
// that a browser has -- printing, the microphone, saving a file made in the page.
public class MainActivity extends Activity {
    static final String BASE = BuildConfig.BASE_URL;
    /** EXTRA_PATH is the page a pressed notification opens. */
    static final String EXTRA_PATH = "path";
    private static final int REQ_FILES = 1, REQ_LOCATION = 2, REQ_NOTIFY = 4, REQ_MIC = 5;
    // The longest file a page may hand over to be saved: a diagram or an
    // export, never a backup.
    private static final int MAX_SAVE = 50 << 20;

    WebView web;
    /** visible: the app is in front, where the page shows what arrives. */
    static volatile boolean visible;
    private ValueCallback<Uri[]> pendingFiles;
    private GeolocationPermissions.Callback pendingGeo;
    private String pendingGeoOrigin;
    private Runnable afterLocation;
    private Runnable afterMic, micDenied;

    @Override
    protected void onCreate(Bundle saved) {
        super.onCreate(saved);
        web = new WebView(this);
        setContentView(web);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setDatabaseEnabled(true);
        s.setGeolocationEnabled(true);
        s.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setMediaPlaybackRequiresUserGesture(false);
        // The pages can tell they are in the app (to offer its update and its
        // own notifications) by this, and by the bridge being there at all.
        s.setUserAgentString(s.getUserAgentString() + " DocfindAndroid/" + BuildConfig.VERSION_NAME);

        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, false);

        web.addJavascriptInterface(new Bridge(this), "DocfindAndroid");
        web.setWebViewClient(new Client());
        web.setWebChromeClient(new Chrome());
        web.setDownloadListener((url, ua, disposition, mime, length) -> download(url, ua, disposition, mime));

        if (saved != null) {
            web.restoreState(saved);
        } else {
            String path = pathOf(getIntent());
            Uri link = getIntent() == null ? null : getIntent().getData();
            web.loadUrl(path != null ? BASE + path : link != null && isOurs(link) ? link.toString() : BASE + "/");
        }
        askNotifications();
        NotifyJob.schedule(this);
        LiveService.start(this);
        askBattery();
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        String path = pathOf(intent);
        if (path != null) web.loadUrl(BASE + path);
    }

    @Override
    protected void onResume() {
        super.onResume();
        visible = true;
        // Opening the app is seeing what is new: move the mark forward without
        // a notification, so nothing on this screen rings later.
        new Thread(() -> NotifyJob.poll(getApplicationContext(), false), "docfind-seen").start();
    }

    /** pathOf is the page a pressed notification asks for: a path here, nothing else. */
    static String pathOf(Intent i) {
        String p = i == null ? null : i.getStringExtra(EXTRA_PATH);
        return p != null && p.startsWith("/") && !p.startsWith("//") ? p : null;
    }

    @Override
    protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        web.saveState(out);
    }

    @Override
    public void onBackPressed() {
        if (web.canGoBack()) web.goBack();
        else super.onBackPressed();
    }

    @Override
    protected void onPause() {
        super.onPause();
        visible = false;
        CookieManager.getInstance().flush(); // the session survives the app being killed
    }

    /** isOurs is whether a link is docfind's own, the only place the app goes. */
    static boolean isOurs(Uri u) {
        Uri base = Uri.parse(BASE);
        return u != null && "https".equals(u.getScheme()) && base.getHost().equalsIgnoreCase(u.getHost());
    }

    private class Client extends WebViewClient {
        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
            Uri u = req.getUrl();
            if (isOurs(u)) return false;
            // Everything else -- another site, tel:, mailto:, a map -- opens
            // outside, where the phone has an app for it.
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, u));
            } catch (Exception e) {
                Toast.makeText(MainActivity.this, u.toString(), Toast.LENGTH_SHORT).show();
            }
            return true;
        }

        @Override
        public void onPageStarted(WebView view, String url, android.graphics.Bitmap icon) {
            Bridge.currentUrl = url;
        }

        @Override
        public void onPageFinished(WebView view, String url) {
            if (isOurs(Uri.parse(url))) view.evaluateJavascript(SHIM, null);
        }

        @Override
        public void onReceivedError(WebView view, WebResourceRequest req, WebResourceError err) {
            if (req.isForMainFrame()) {
                String msg = getString(R.string.offline);
                String html = "<html dir='rtl'><body style='font-family:sans-serif;background:#142F40;color:#fff;"
                        + "display:flex;flex-direction:column;align-items:center;justify-content:center;height:90vh'>"
                        + "<p>" + msg + "</p><button onclick='location.reload()' style='padding:10px 24px'>"
                        + getString(R.string.retry) + "</button></body></html>";
                view.loadDataWithBaseURL(BASE + "/", html, "text/html", "utf-8", req.getUrl().toString());
            }
        }
    }

    private class Chrome extends WebChromeClient {
        @Override
        public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> cb, FileChooserParams params) {
            if (pendingFiles != null) pendingFiles.onReceiveValue(null);
            pendingFiles = cb;
            try {
                startActivityForResult(params.createIntent(), REQ_FILES);
            } catch (Exception e) {
                pendingFiles = null;
                return false;
            }
            return true;
        }

        // The microphone (voice messages, calls) and the camera (video calls,
        // round video messages): only for the messenger's own pages, only
        // those two, and only once Android has said yes to each one asked for.
        @Override
        public void onPermissionRequest(PermissionRequest req) {
            boolean audio = false, video = false;
            for (String r : req.getResources()) {
                if (PermissionRequest.RESOURCE_AUDIO_CAPTURE.equals(r)) audio = true;
                if (PermissionRequest.RESOURCE_VIDEO_CAPTURE.equals(r)) video = true;
            }
            if ((!audio && !video) || !isOurs(req.getOrigin())) {
                req.deny();
                return;
            }
            final java.util.List<String> grant = new java.util.ArrayList<>();
            if (audio) grant.add(PermissionRequest.RESOURCE_AUDIO_CAPTURE);
            if (video) grant.add(PermissionRequest.RESOURCE_VIDEO_CAPTURE);
            final boolean a = audio, v = video;
            runOnUiThread(() -> withMedia(a, v,
                    () -> req.grant(grant.toArray(new String[0])), req::deny));
        }

        @Override
        public void onGeolocationPermissionsShowPrompt(String origin, GeolocationPermissions.Callback cb) {
            if (!isOurs(Uri.parse(origin))) {
                cb.invoke(origin, false, false);
                return;
            }
            pendingGeo = cb;
            pendingGeoOrigin = origin;
            withLocation(() -> {
                if (pendingGeo != null) pendingGeo.invoke(pendingGeoOrigin, true, false);
                pendingGeo = null;
            });
        }
    }

    @Override
    protected void onActivityResult(int req, int result, Intent data) {
        super.onActivityResult(req, result, data);
        if (req == REQ_FILES && pendingFiles != null) {
            pendingFiles.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result, data));
            pendingFiles = null;
        }
    }

    /** download hands a file to the phone's downloads, with the session's cookie. */
    private void download(String url, String ua, String disposition, String mime) {
        Uri u = Uri.parse(url);
        if (!isOurs(u)) {
            startActivity(new Intent(Intent.ACTION_VIEW, u));
            return;
        }
        String name = URLUtil.guessFileName(url, disposition, mime);
        DownloadManager.Request r = new DownloadManager.Request(u)
                .addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url))
                .addRequestHeader("User-Agent", ua)
                .setMimeType(mime)
                .setTitle(name)
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
        ((DownloadManager) getSystemService(DOWNLOAD_SERVICE)).enqueue(r);
        Toast.makeText(this, name, Toast.LENGTH_SHORT).show();
    }

    // ------------------------------------------------- what a WebView lacks

    // SHIM is run in every page of the messenger once it has loaded. A WebView
    // ignores window.print(), and its download manager cannot fetch a blob:
    // link, which is how a page saves a file it made itself. Both go to Bridge.
    private static final String SHIM = "(function(){"
            + "if(window.__docfindShim||!window.DocfindAndroid)return;window.__docfindShim=1;"
            + "var A=window.DocfindAndroid;"
            + "window.print=function(){A.print(document.title||'')};"
            + "function save(a){var name=a.getAttribute('download')||'download';"
            + "fetch(a.href).then(function(r){return r.blob()}).then(function(b){"
            + "var f=new FileReader();f.onload=function(){var s=String(f.result);"
            + "A.saveFile(name,b.type||'application/octet-stream',s.slice(s.indexOf(',')+1))};"
            + "f.readAsDataURL(b)})}"
            + "function mine(a){return a&&a.hasAttribute('download')&&/^(blob|data):/.test(a.href)}"
            + "var click=HTMLAnchorElement.prototype.click;"
            + "HTMLAnchorElement.prototype.click=function(){if(mine(this)){save(this);return}return click.call(this)};"
            + "document.addEventListener('click',function(e){var a=e.target&&e.target.closest&&e.target.closest('a[download]');"
            + "if(mine(a)){e.preventDefault();save(a)}},true);"
            + "})();";

    /**
     * printPage hands the page to Android's printing, whose "Save as PDF" is
     * the export. The page is told when it is over (afterprint), as a browser
     * would, so it puts its charts back the way the screen shows them.
     */
    void printPage(String title) {
        PrintManager pm = (PrintManager) getSystemService(PRINT_SERVICE);
        if (pm == null) return;
        String job = title == null || title.trim().isEmpty() ? getString(R.string.app_name) : title.trim();
        final PrintDocumentAdapter inner = web.createPrintDocumentAdapter(job);
        PrintDocumentAdapter outer = new PrintDocumentAdapter() {
            @Override
            public void onStart() {
                inner.onStart();
            }

            @Override
            public void onLayout(PrintAttributes oldA, PrintAttributes newA, CancellationSignal cancel,
                                 LayoutResultCallback cb, Bundle extras) {
                inner.onLayout(oldA, newA, cancel, cb, extras);
            }

            @Override
            public void onWrite(PageRange[] pages, ParcelFileDescriptor dest, CancellationSignal cancel,
                                WriteResultCallback cb) {
                inner.onWrite(pages, dest, cancel, cb);
            }

            @Override
            public void onFinish() {
                inner.onFinish();
                web.evaluateJavascript("window.dispatchEvent(new Event('afterprint'))", null);
            }
        };
        pm.print(job, outer, new PrintAttributes.Builder().build());
    }

    /** saveFile puts a file a page made into the phone's Downloads. */
    void saveFile(String name, String mime, String base64) {
        byte[] data;
        try {
            data = Base64.decode(base64 == null ? "" : base64, Base64.DEFAULT);
        } catch (IllegalArgumentException e) {
            say(getString(R.string.save_failed));
            return;
        }
        if (data.length == 0 || data.length > MAX_SAVE) {
            say(getString(R.string.save_failed));
            return;
        }
        String file = cleanName(name);
        String type = mime != null && mime.matches("[a-zA-Z0-9.+-]+/[a-zA-Z0-9.+-]+") ? mime : "application/octet-stream";
        try {
            if (Build.VERSION.SDK_INT >= 29) {
                ContentValues v = new ContentValues();
                v.put(MediaStore.Downloads.DISPLAY_NAME, file);
                v.put(MediaStore.Downloads.MIME_TYPE, type);
                v.put(MediaStore.Downloads.IS_PENDING, 1);
                Uri uri = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v);
                if (uri == null) throw new IllegalStateException("no row");
                try (OutputStream o = getContentResolver().openOutputStream(uri)) {
                    if (o == null) throw new IllegalStateException("no stream");
                    o.write(data);
                }
                v.clear();
                v.put(MediaStore.Downloads.IS_PENDING, 0);
                getContentResolver().update(uri, v, null, null);
            } else {
                // Before Android 10 the shared Downloads needs a storage
                // permission this app does not ask for; its own folder does not.
                File dir = getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS);
                if (dir == null) throw new IllegalStateException("no folder");
                try (FileOutputStream o = new FileOutputStream(new File(dir, file))) {
                    o.write(data);
                }
            }
            say(getString(R.string.saved, file));
        } catch (Exception e) {
            say(getString(R.string.save_failed));
        }
    }

    /** cleanName is a file name with nothing in it that could be a path. */
    static String cleanName(String name) {
        String n = name == null ? "" : name.replaceAll("[\\\\/:*?\"<>|\\p{Cntrl}]", "_").trim();
        while (n.startsWith(".")) n = n.substring(1);
        if (n.isEmpty()) n = "download";
        return n.length() > 120 ? n.substring(n.length() - 120) : n;
    }

    private void say(String text) {
        runOnUiThread(() -> Toast.makeText(this, text, Toast.LENGTH_SHORT).show());
    }

    // ------------------------------------------------------------ permissions

    /** withMedia runs then once the microphone (audio) and the camera (video)
     *  are allowed, otherwise no. */
    void withMedia(boolean audio, boolean video, Runnable then, Runnable no) {
        java.util.List<String> need = new java.util.ArrayList<>();
        if (audio && checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED)
            need.add(Manifest.permission.RECORD_AUDIO);
        if (video && checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED)
            need.add(Manifest.permission.CAMERA);
        if (need.isEmpty()) {
            then.run();
            return;
        }
        if (micDenied != null) micDenied.run();
        afterMic = then;
        micDenied = no;
        requestPermissions(need.toArray(new String[0]), REQ_MIC);
    }

    /** withLocation runs then once the location permission is granted. */
    void withLocation(Runnable then) {
        if (checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED) {
            then.run();
            return;
        }
        afterLocation = then;
        requestPermissions(new String[]{Manifest.permission.ACCESS_FINE_LOCATION,
                Manifest.permission.ACCESS_COARSE_LOCATION}, REQ_LOCATION);
    }

    // Once: may the app stay connected with the battery saver on? Without it,
    // phones (Xiaomi's above all) stop the connection and calls stop ringing.
    private void askBattery() {
        if (Build.VERSION.SDK_INT < 23) return;
        android.os.PowerManager pm = (android.os.PowerManager) getSystemService(POWER_SERVICE);
        android.content.SharedPreferences p = getSharedPreferences("notify", MODE_PRIVATE);
        if (pm == null || pm.isIgnoringBatteryOptimizations(getPackageName()) || p.getBoolean("askedBattery", false)) return;
        p.edit().putBoolean("askedBattery", true).apply();
        try {
            startActivity(new Intent(android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + getPackageName())));
        } catch (Exception ignored) {
        }
    }

    private void askNotifications() {
        if (Build.VERSION.SDK_INT >= 33
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTIFY);
        }
    }

    @Override
    public void onRequestPermissionsResult(int req, String[] perms, int[] results) {
        boolean granted = results.length > 0 && results[0] == PackageManager.PERMISSION_GRANTED;
        if (req == REQ_MIC) {
            // Every permission asked for must have been granted.
            boolean all = results.length > 0;
            for (int x : results) all &= x == PackageManager.PERMISSION_GRANTED;
            Runnable r = all ? afterMic : micDenied;
            afterMic = null;
            micDenied = null;
            if (r != null) r.run();
            return;
        }
        if (req == REQ_LOCATION) {
            Runnable then = afterLocation;
            afterLocation = null;
            if (granted && then != null) then.run();
            if (!granted && pendingGeo != null) {
                pendingGeo.invoke(pendingGeoOrigin, false, false);
                pendingGeo = null;
            }
        }
    }

    @Override
    protected void onDestroy() {
        web.setVisibility(View.GONE);
        super.onDestroy();
    }
}
