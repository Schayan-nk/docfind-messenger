package ir.schayan.messenger;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.job.JobInfo;
import android.app.job.JobParameters;
import android.app.job.JobScheduler;
import android.app.job.JobService;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.media.AudioAttributes;
import android.media.RingtoneManager;
import android.os.Build;
import android.webkit.CookieManager;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.UUID;

// The messenger's notifications on the phone, with the app closed.
//
// A WebView has no Push API. LiveService keeps a request open to the server
// (poll with wait) so calls and messages arrive within seconds; this job, every
// quarter of an hour, is the fallback that also starts LiveService again if
// the phone stopped it. Signed in as the person by the WebView's own session
// cookie: no second credential is kept on the phone.
public class NotifyJob extends JobService {
    private static final int JOB_ID = 7;
    private static final String PREFS = "notify";
    static final String CH_IMPORTANT = "important", CH_INFO = "info", CH_QUIET = "quiet", CH_RING = "ring";

    /** schedule makes sure the periodic job exists; scheduling twice is harmless. */
    static void schedule(Context c) {
        JobScheduler js = (JobScheduler) c.getSystemService(Context.JOB_SCHEDULER_SERVICE);
        if (js == null || js.getPendingJob(JOB_ID) != null) return;
        js.schedule(new JobInfo.Builder(JOB_ID, new ComponentName(c, NotifyJob.class))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setPeriodic(15 * 60 * 1000L)
                .setPersisted(true)
                .build());
    }

    @Override
    public boolean onStartJob(JobParameters params) {
        if (!LiveService.running) LiveService.start(getApplicationContext());
        new Thread(() -> {
            if (!LiveService.connected()) poll(getApplicationContext(), true, 0);
            jobFinished(params, false);
        }, "messenger-notify").start();
        return true;
    }

    @Override
    public boolean onStopJob(JobParameters params) {
        return true;
    }

    static void poll(Context c, boolean show) {
        poll(c, show, 0);
    }

    /**
     * poll asks once, holding up to wait seconds for something new; true when
     * the server answered. With show false it only moves the mark forward: the
     * app has just been opened, and what is on the screen needs no
     * notification. While the app is in front nothing is shown either: the
     * page shows it.
     */
    static synchronized boolean poll(Context c, boolean show, int wait) {
        SharedPreferences p = c.getSharedPreferences(PREFS, MODE_PRIVATE);
        String device = p.getString("device", null);
        if (device == null) {
            device = UUID.randomUUID().toString();
            p.edit().putString("device", device).apply();
        }
        long after = p.getLong("last", 0);
        JSONObject answer;
        try {
            JSONObject req = new JSONObject();
            req.put("device", device);
            req.put("label", (Build.MANUFACTURER + " " + Build.MODEL).trim());
            req.put("after", after);
            if (wait > 0) req.put("wait", wait);
            String body = post(MainActivity.BASE + "/api/app/poll", req.toString(), wait > 0 ? (wait + 20) * 1000 : 20_000);
            if (body == null) return false;
            answer = new JSONObject(body);
        } catch (Exception e) {
            return false;
        }
        long last = answer.optLong("last", after);
        JSONArray list = answer.optJSONArray("notifications");
        if (show && !MainActivity.visible && list != null && allowed(c)) {
            channels(c);
            for (int i = 0; i < list.length(); i++) {
                JSONObject n = list.optJSONObject(i);
                if (n != null) notify(c, n);
            }
        }
        p.edit().putLong("last", last).apply();
        return true;
    }

    private static boolean allowed(Context c) {
        return Build.VERSION.SDK_INT < 33
                || c.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
    }

    private static void channels(Context c) {
        if (Build.VERSION.SDK_INT < 26) return;
        NotificationManager nm = c.getSystemService(NotificationManager.class);
        nm.createNotificationChannel(new NotificationChannel(CH_IMPORTANT,
                c.getString(R.string.notify_important), NotificationManager.IMPORTANCE_HIGH));
        nm.createNotificationChannel(new NotificationChannel(CH_INFO,
                c.getString(R.string.notify_info), NotificationManager.IMPORTANCE_DEFAULT));
        nm.createNotificationChannel(new NotificationChannel(CH_QUIET,
                c.getString(R.string.notify_quiet), NotificationManager.IMPORTANCE_LOW));
        // A ringing call: the phone's ringtone, again and again, until it is
        // answered, put down or missed.
        NotificationChannel ring = new NotificationChannel(CH_RING, c.getString(R.string.notify_calls), NotificationManager.IMPORTANCE_HIGH);
        ring.setSound(RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE), new AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE)
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION).build());
        ring.enableVibration(true);
        ring.setVibrationPattern(new long[]{0, 900, 700, 900, 700});
        nm.createNotificationChannel(ring);
    }

    // local shows a notification the open page asked for (Bridge.notify): a
    // message or a ringing call heard on the page's own socket. The same tag
    // as the server's, so the server's replaces it rather than adding a second.
    static void local(Context c, String title, String body, String tag, String url, boolean important) {
        if (!allowed(c)) return;
        channels(c);
        try {
            JSONObject n = new JSONObject();
            n.put("title", title == null ? "" : title);
            n.put("body", body == null ? "" : body);
            n.put("tag", tag == null ? "" : tag);
            n.put("url", url == null || !url.startsWith("/") ? "/messages" : url);
            n.put("priority", important ? "important" : "");
            n.put("id", tag == null ? 0 : tag.hashCode() & 0x7fffffff);
            notify(c, n);
        } catch (Exception ignored) {
        }
    }

    // unlocal takes a notification the page showed away (a call that stopped
    // ringing, a chat that was read).
    static void unlocal(Context c, String tag) {
        if (tag == null || tag.isEmpty()) return;
        ((NotificationManager) c.getSystemService(Context.NOTIFICATION_SERVICE)).cancel(tag, 0);
    }

    private static void notify(Context c, JSONObject n) {
        long id = n.optLong("id");
        String tag = n.optString("tag", "");
        String url = n.optString("url", "/");
        boolean ringing = tag.startsWith("call-") && url.contains("#call");
        String channel = ringing ? CH_RING : n.optBoolean("quiet") ? CH_QUIET
                : "important".equals(n.optString("priority")) ? CH_IMPORTANT : CH_INFO;
        Intent open = new Intent(c, MainActivity.class)
                .setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP)
                .putExtra(MainActivity.EXTRA_PATH, url);
        int nid = (int) (id % Integer.MAX_VALUE);
        PendingIntent pi = PendingIntent.getActivity(c, nid, open,
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        Notification.Builder b = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(c, channel)
                : new Notification.Builder(c);
        b.setContentTitle(n.optString("title"))
                .setContentText(n.optString("body"))
                .setStyle(new Notification.BigTextStyle().bigText(n.optString("body")))
                .setSmallIcon(ringing ? android.R.drawable.sym_call_incoming : android.R.drawable.stat_notify_chat)
                .setContentIntent(pi)
                .setAutoCancel(true);
        if (ringing) {
            // Over the lock screen and full-screen where the phone allows, and
            // gone by itself once the call can no longer be answered.
            b.setCategory(Notification.CATEGORY_CALL).setFullScreenIntent(pi, true).setOngoing(true);
            if (Build.VERSION.SDK_INT >= 26) b.setTimeoutAfter(45_000);
        } else {
            b.setCategory(Notification.CATEGORY_MESSAGE);
        }
        Notification note = b.build();
        if (ringing) note.flags |= Notification.FLAG_INSISTENT;
        // The server's tag replaces rather than stacks: a missed call takes the
        // ringing one's place, a newer message in a chat the older one's.
        NotificationManager nm = (NotificationManager) c.getSystemService(Context.NOTIFICATION_SERVICE);
        if (tag.isEmpty()) nm.notify(nid, note);
        else nm.notify(tag, 0, note);
    }

    /** post sends body as JSON with the session cookie; the answer on 200, else null. */
    private static String post(String url, String body, int readTimeout) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(url).openConnection();
            c.setConnectTimeout(15_000);
            c.setReadTimeout(readTimeout);
            c.setRequestMethod("POST");
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json");
            c.setRequestProperty("Origin", MainActivity.BASE);
            String cookie = CookieManager.getInstance().getCookie(MainActivity.BASE);
            if (cookie == null || cookie.isEmpty()) return null; // never signed in
            c.setRequestProperty("Cookie", cookie);
            try (OutputStream o = c.getOutputStream()) {
                o.write(body.getBytes(StandardCharsets.UTF_8));
            }
            if (c.getResponseCode() != 200) return null;
            try (InputStream in = c.getInputStream()) {
                ByteArrayOutputStream out = new ByteArrayOutputStream();
                byte[] buf = new byte[8192];
                int n;
                while ((n = in.read(buf)) > 0 && out.size() < (256 << 10)) out.write(buf, 0, n);
                return out.toString("UTF-8");
            }
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }
}
