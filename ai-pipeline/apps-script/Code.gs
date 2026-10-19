/**
 * AI Bug-Fix Pipeline — Apps Script ฝั่ง Google Form
 *
 * ผูก script นี้กับ Google Sheet ที่เก็บคำตอบของฟอร์ม (Extensions → Apps Script) แล้ว:
 *   1. ตั้ง Script Properties (Project Settings → Script properties):
 *        GITHUB_OWNER, GITHUB_REPO   repo ที่จะส่งรายงานไป
 *        GITHUB_TOKEN                fine-grained PAT เฉพาะ repo นี้ สิทธิ์ Contents: Read and write อย่างเดียว
 *                                    (endpoint dispatches ต้องการสิทธิ์นี้)
 *        NOTIFY_SECRET               ค่าเดียวกับ secret APPS_SCRIPT_NOTIFY_SECRET ใน GitHub
 *        MAX_REPORTS_PER_DAY         (ไม่บังคับ) ค่าเริ่มต้น 5
 *   2. รัน setup() หนึ่งครั้ง เพื่อสร้าง trigger onFormSubmit และ sheet "Reports"
 *   3. Deploy → New deployment → Web app: Execute as "Me", Who has access "Anyone"
 *      แล้วนำ URL ไปตั้งเป็น variable APPS_SCRIPT_NOTIFY_URL ใน GitHub
 *
 * อีเมลผู้แจ้งเก็บอยู่ใน sheet "Reports" เท่านั้น ไม่ส่งไป GitHub (ส่งแค่ report_id)
 */

// ชื่อคำถามในฟอร์ม ต้องตรงกับหัวคอลัมน์ใน sheet คำตอบทุกตัวอักษร
var QUESTIONS = {
  page: 'หน้า/ฟีเจอร์ที่เกิดปัญหา',
  steps: 'ขั้นตอนที่ทำให้เกิดปัญหา',
  expected: 'ผลที่คาดหวัง',
  actual: 'ผลที่เกิดขึ้นจริง',
  severity: 'ความรุนแรง',
  environment: 'อุปกรณ์/Browser/OS',
  screenshot: 'ภาพหน้าจอ (ไม่บังคับ)',
};
var REQUIRED = ['page', 'steps', 'expected', 'actual', 'severity'];
var EMAIL_COLUMN = 'Email Address'; // คอลัมน์ที่ฟอร์มเก็บอีเมลอัตโนมัติ (ภาษาไทย: "ที่อยู่อีเมล")
var MAX_FIELD_LENGTH = 5000;
var REPORTS_SHEET = 'Reports';
var REPORTS_HEADER = ['report_id', 'email', 'submitted_at', 'status', 'updated_at'];

function setup() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  ScriptApp.getProjectTriggers().forEach(function (t) {
    if (t.getHandlerFunction() === 'onFormSubmit') ScriptApp.deleteTrigger(t);
  });
  ScriptApp.newTrigger('onFormSubmit').forSpreadsheet(ss).onFormSubmit().create();
  reportsSheet_();
}

// ---- ขาเข้า: ฟอร์มถูกส่ง → ตรวจ → ส่ง repository_dispatch ไป GitHub ----
function onFormSubmit(e) {
  var values = e.namedValues;
  var email = first_(values[EMAIL_COLUMN] || values['ที่อยู่อีเมล']);
  if (!email) {
    console.error('ไม่พบอีเมลผู้แจ้ง ตรวจว่าฟอร์มเปิด "เก็บอีเมล" และ EMAIL_COLUMN ถูกต้อง');
    return;
  }

  var report = {};
  var missing = [];
  Object.keys(QUESTIONS).forEach(function (key) {
    var value = first_(values[QUESTIONS[key]]).trim();
    if (!value && REQUIRED.indexOf(key) >= 0) missing.push(QUESTIONS[key]);
    report[key] = value.slice(0, MAX_FIELD_LENGTH);
  });
  if (missing.length) {
    mail_(email, 'ส่งรายงานปัญหาไม่สำเร็จ', 'กรุณากรอกข้อมูลให้ครบ: ' + missing.join(', '));
    return;
  }

  var lock = LockService.getScriptLock();
  lock.waitLock(30000);
  var reportId;
  try {
    var limit = Number(prop_('MAX_REPORTS_PER_DAY', '5'));
    if (countRecent_(email, 24 * 60 * 60 * 1000) >= limit) {
      mail_(email, 'ส่งรายงานปัญหาไม่สำเร็จ',
        'คุณส่งรายงานครบ ' + limit + ' ครั้งใน 24 ชั่วโมงแล้ว กรุณาลองใหม่ภายหลัง');
      return;
    }
    reportId = Utilities.formatDate(new Date(), 'UTC', 'yyyyMMdd') + '-' +
      Utilities.getUuid().replace(/-/g, '').slice(0, 12);
    reportsSheet_().appendRow([reportId, email, new Date(), 'submitted', new Date()]);
  } finally {
    lock.releaseLock();
  }

  // client_payload มี top-level property ได้ไม่เกิน 10 ตัว และห้ามมีอีเมลผู้แจ้ง
  var payload = {
    event_type: 'bug_report',
    client_payload: {
      report_id: reportId,
      page: report.page,
      steps: report.steps,
      expected: report.expected,
      actual: report.actual,
      severity: report.severity,
      environment: report.environment,
      screenshot_url: report.screenshot,
      submitted_at: new Date().toISOString(),
    },
  };
  var res = UrlFetchApp.fetch(
    'https://api.github.com/repos/' + prop_('GITHUB_OWNER') + '/' + prop_('GITHUB_REPO') + '/dispatches',
    {
      method: 'post',
      contentType: 'application/json',
      headers: {
        Authorization: 'Bearer ' + prop_('GITHUB_TOKEN'),
        Accept: 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28',
      },
      payload: JSON.stringify(payload),
      muteHttpExceptions: true,
    });
  if (res.getResponseCode() !== 204) {
    setStatus_(reportId, 'dispatch_failed');
    console.error('dispatch ไม่สำเร็จ ' + res.getResponseCode() + ': ' + res.getContentText());
    mail_(email, 'ได้รับรายงานปัญหาแล้ว (' + reportId + ')',
      'ระบบส่งรายงานต่ออัตโนมัติไม่สำเร็จ ทีมงานจะตรวจสอบให้');
    return;
  }
  setStatus_(reportId, 'dispatched');
  mail_(email, 'ได้รับรายงานปัญหาแล้ว (' + reportId + ')',
    'ขอบคุณที่แจ้งปัญหา เราจะแจ้งผลให้ทราบทางอีเมลนี้\nหมายเลขรายงาน: ' + reportId);
}

// ---- ขาออก: GitHub workflow เรียกมาเพื่อแจ้งผลให้ผู้แจ้ง ----
// body: {secret, report_id, kind: need_info|duplicate|not_bug|fixed, message}
// Apps Script web app อ่าน header ไม่ได้ จึงใช้ secret ใน body
var SUBJECTS = {
  need_info: 'ขอข้อมูลเพิ่มเติมเกี่ยวกับปัญหาที่คุณแจ้ง',
  duplicate: 'ปัญหาที่คุณแจ้งมีผู้แจ้งไว้แล้ว',
  not_bug: 'ผลการตรวจสอบปัญหาที่คุณแจ้ง',
  fixed: 'ปัญหาที่คุณแจ้งได้รับการแก้ไขแล้ว',
};

function doPost(e) {
  var body;
  try {
    body = JSON.parse(e.postData.contents);
  } catch (err) {
    return json_({ ok: false, error: 'bad_json' });
  }
  if (!safeEqual_(String(body.secret || ''), prop_('NOTIFY_SECRET'))) {
    return json_({ ok: false, error: 'unauthorized' });
  }
  var reportId = String(body.report_id || '');
  var kind = String(body.kind || '');
  if (!/^[A-Za-z0-9_-]{1,64}$/.test(reportId) || !SUBJECTS.hasOwnProperty(kind)) {
    return json_({ ok: false, error: 'bad_request' });
  }

  var row = findRow_(reportId);
  if (!row) return json_({ ok: false, error: 'unknown_report' });
  var email = reportsSheet_().getRange(row, 2).getValue();
  var message = String(body.message || '').slice(0, MAX_FIELD_LENGTH);

  mail_(email, SUBJECTS[kind] + ' (' + reportId + ')', message);
  setStatus_(reportId, kind);
  return json_({ ok: true });
}

// ---- helpers ----
function reportsSheet_() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName(REPORTS_SHEET);
  if (!sheet) {
    sheet = ss.insertSheet(REPORTS_SHEET);
    sheet.appendRow(REPORTS_HEADER);
    sheet.setFrozenRows(1);
  }
  return sheet;
}

function countRecent_(email, windowMs) {
  var since = Date.now() - windowMs;
  var rows = reportsSheet_().getDataRange().getValues().slice(1);
  return rows.filter(function (r) {
    return r[1] === email && r[2] instanceof Date && r[2].getTime() >= since;
  }).length;
}

function findRow_(reportId) {
  var ids = reportsSheet_().getRange('A:A').getValues();
  for (var i = 1; i < ids.length; i++) {
    if (ids[i][0] === reportId) return i + 1;
  }
  return 0;
}

function setStatus_(reportId, status) {
  var row = findRow_(reportId);
  if (row) reportsSheet_().getRange(row, 4, 1, 2).setValues([[status, new Date()]]);
}

function mail_(to, subject, text) {
  MailApp.sendEmail({ to: to, subject: subject, body: text });
}

function prop_(name, fallback) {
  var value = PropertiesService.getScriptProperties().getProperty(name);
  if (value) return value;
  if (fallback !== undefined) return fallback;
  throw new Error('Script property ' + name + ' is not set');
}

function first_(arr) {
  return arr && arr.length ? String(arr[0]) : '';
}

function safeEqual_(a, b) {
  if (!a || a.length !== b.length) return false;
  var diff = 0;
  for (var i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function json_(obj) {
  return ContentService.createTextOutput(JSON.stringify(obj)).setMimeType(ContentService.MimeType.JSON);
}
