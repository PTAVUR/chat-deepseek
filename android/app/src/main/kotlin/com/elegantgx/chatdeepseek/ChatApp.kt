package com.elegantgx.chatdeepseek

import android.app.Application

class ChatApp : Application() {
  lateinit var server: ServerManager
    private set

  override fun onCreate() {
    super.onCreate()
    server = ServerManager(this)
  }
}
