package com.example.logi_track_driver_app

import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import android.os.Bundle
import io.flutter.embedding.android.FlutterActivity

class MainActivity : FlutterActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        createNotificationChannels()
    }

    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val chatChannel = NotificationChannel(
                "chat",
                "Chat messages",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Notifications for new messages from admin"
                setShowBadge(true)
                enableVibration(true)
                // ใช้เสียงแจ้งเตือนมาตรฐานของระบบ (ไม่ตั้ง setSound = default)
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager?.createNotificationChannel(chatChannel)
        }
    }
}
