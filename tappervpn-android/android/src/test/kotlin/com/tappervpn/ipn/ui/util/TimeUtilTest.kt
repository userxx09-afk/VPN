// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package com.tappervpn.ipn.ui.util

import com.tappervpn.ipn.util.TSLog
import com.tappervpn.ipn.util.TSLog.LibtappervpnWrapper
import java.time.Duration
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.mockito.ArgumentMatchers.anyString
import org.mockito.Mockito.doNothing
import org.mockito.Mockito.mock

class TimeUtilTest {

  private lateinit var libtappervpnWrapperMock: LibtappervpnWrapper
  private lateinit var originalWrapper: LibtappervpnWrapper

  @Before
  fun setUp() {
    libtappervpnWrapperMock = mock(LibtappervpnWrapper::class.java)
    doNothing().`when`(libtappervpnWrapperMock).sendLog(anyString(), anyString())

    // Store the original wrapper so we can reset it later
    originalWrapper = TSLog.libtappervpnWrapper
    // Inject mock into TSLog
    TSLog.libtappervpnWrapper = libtappervpnWrapperMock
  }

  @After
  fun tearDown() {
    // Reset TSLog after each test to avoid side effects
    TSLog.libtappervpnWrapper = originalWrapper
  }

  @Test
  fun durationInvalidMsUnits() {
    val input = "5s10ms"
    val actual = TimeUtil.duration(input)
    assertNull("Should return null", actual)
  }

  @Test
  fun durationInvalidUsUnits() {
    val input = "5s10us"
    val actual = TimeUtil.duration(input)
    assertNull("Should return null", actual)
  }

  @Test
  fun durationTestHappyPath() {
    val input = arrayOf("1.0y1.0w1.0d1.0h1.0m1.0s", "1s", "1m", "1h", "1d", "1w", "1y")
    val expectedSeconds =
        arrayOf((31536000 + 604800 + 86400 + 3600 + 60 + 1), 1, 60, 3600, 86400, 604800, 31536000)
    val expected = expectedSeconds.map { Duration.ofSeconds(it.toLong()) }
    val actual = input.map { TimeUtil.duration(it) }
    assertEquals("Incorrect conversion", expected, actual)
  }

  @Test
  fun testBadDurationString() {
    val input = "1..0y1.0w1.0d1.0h1.0m1.0s"
    val actual = TimeUtil.duration(input)
    assertNull("Should return null", actual)
  }

  @Test
  fun testBadDInputString() {
    val libtappervpnWrapperMock = mock(LibtappervpnWrapper::class.java)
    doNothing().`when`(libtappervpnWrapperMock).sendLog(anyString(), anyString())

    val input = "1.0yy1.0w1.0d1.0h1.0m1.0s"
    val actual = TimeUtil.duration(input)
    assertNull("Should return null", actual)
  }

  @Test
  fun testIgnoreFractionalSeconds() {
    val input = "10.9s"
    val expectedSeconds = 10
    val expected = Duration.ofSeconds(expectedSeconds.toLong())
    val actual = TimeUtil.duration(input)
    assertEquals("Should return $expectedSeconds seconds", expected, actual)
  }
}
