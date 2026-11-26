package com.elegantgx.chatdeepseek

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import java.util.concurrent.Executors

/**
 * Keeps the local Go server process alive while the app is in the
 * background. The server itself still belongs to ChatApp/ServerManager;
 * this service only pins the app process with an ongoing notification.
 */
class ServerService : Service() {
  companion object {
    private const val TAG = "ChatDeepseek"
    private const val CHANNEL_ID = "chat-deepseek-server"
    private const val NOTIFICATION_ID = 1001
    private const val ACTION_STOP = "com.elegantgx.chatdeepseek.action.STOP_SERVER"

    fun start(context: Context) {
      ContextCompat.startForegroundService(context, Intent(context, ServerService::class.java))
    }
  }

  private val executor = Executors.newSingleThreadExecutor()
  private lateinit var server: ServerManager

  override fun onCreate() {
    super.onCreate()
    server = (application as ChatApp).server
    createNotificationChannel()
    startInForeground(getString(R.string.server_notification_running))
  }

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    if (intent?.action == ACTION_STOP) {
      executor.execute {
        server.stop()
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
      }
      return START_NOT_STICKY
    }

    startInForeground(getString(R.string.server_notification_running))
    executor.execute {
      if (!server.isInitialized()) {
        stopSelf()
        return@execute
      }
      runCatching { server.start() }.onFailure { error ->
        Log.e(TAG, "foreground server start failed", error)
        updateNotification(getString(R.string.server_notification_failed))
      }
    }
    return START_STICKY
  }

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onDestroy() {
    executor.shutdownNow()
    super.onDestroy()
  }

  private fun startInForeground(text: String) {
    val type =
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
        ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
      } else {
        0
      }
    ServiceCompat.startForeground(this, NOTIFICATION_ID, buildNotification(text), type)
  }

  private fun updateNotification(text: String) {
    runCatching {
      getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, buildNotification(text))
    }
  }

  private fun buildNotification(text: String): Notification {
    val openIntent =
      Intent(this, MainActivity::class.java).apply {
        flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
      }
    val openPending =
      PendingIntent.getActivity(
        this,
        0,
        openIntent,
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
      )
    val stopIntent = Intent(this, ServerService::class.java).setAction(ACTION_STOP)
    val stopPending =
      PendingIntent.getService(
        this,
        1,
        stopIntent,
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
      )
    return NotificationCompat.Builder(this, CHANNEL_ID)
      .setSmallIcon(R.drawable.whale)
      .setContentTitle(getString(R.string.app_name))
      .setContentText(text)
      .setContentIntent(openPending)
      .setOngoing(true)
      .setOnlyAlertOnce(true)
      .setCategory(NotificationCompat.CATEGORY_SERVICE)
      .setPriority(NotificationCompat.PRIORITY_LOW)
      .addAction(0, getString(R.string.server_notification_stop), stopPending)
      .build()
  }

  private fun createNotificationChannel() {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
      return
    }
    val manager = getSystemService(NotificationManager::class.java)
    val channel =
      NotificationChannel(
        CHANNEL_ID,
        getString(R.string.server_notification_channel),
        NotificationManager.IMPORTANCE_LOW,
      )
        .apply {
          description = getString(R.string.server_notification_channel_description)
          setShowBadge(false)
        }
    manager.createNotificationChannel(channel)
  }
}
