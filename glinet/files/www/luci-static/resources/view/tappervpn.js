'use strict';
'require view';
'require form';
'require fs';
'require network';

// Parses `tappervpn exit-node list`: rows of "IP  HOSTNAME  COUNTRY  CITY  STATUS".
function parseExitNodes(out) {
	var nodes = [];
	(out || '').split('\n').forEach(function(line) {
		var f = line.trim().split(/\s+/);
		if (f.length >= 2 && /^\d+\.\d+\.\d+\.\d+$/.test(f[0]))
			nodes.push({ ip: f[0], name: f[1].split('.')[0], status: f.slice(4).join(' ') });
	});
	return nodes;
}

function run(args) {
	return fs.exec('/usr/sbin/tappervpn', args).then(function(res) {
		return (res.stdout || '') + (res.stderr || '');
	}).catch(function(err) {
		return String(err);
	});
}

return view.extend({
	load: function() {
		return Promise.all([
			run([ 'status' ]),
			run([ 'exit-node', 'list' ]),
			L.resolveDefault(network.getHostHints(), null)
		]);
	},

	render: function(data) {
		var status = data[0], exitNodes = parseExitNodes(data[1]), hosts = data[2];
		var m, s, o;

		m = new form.Map('tappervpn', _('TapperVPN'),
			_('Connects this router to your TapperVPN network and sends chosen LAN traffic through an exit node, ' +
			  'for devices that cannot run the app (smart TVs, consoles).'));

		s = m.section(form.NamedSection, 'main', 'tappervpn', _('Status'));
		o = s.option(form.DummyValue, '_status');
		o.rawhtml = true;
		o.cfgvalue = function() {
			return E('pre', { 'style': 'white-space:pre-wrap;margin:0' }, status.trim() || _('Not running'));
		};

		s = m.section(form.NamedSection, 'main', 'tappervpn', _('Settings'));

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.rmempty = false;

		o = s.option(form.Value, 'login_server', _('Server address'),
			_('Your TapperVPN control server, e.g. https://vpn.example.com'));
		o.placeholder = 'https://vpn.example.com';
		o.rmempty = false;

		o = s.option(form.Value, 'auth_key', _('Sign-in key'),
			_('Pre-auth key from the server admin. Only needed for the first sign-in.'));
		o.password = true;

		o = s.option(form.Value, 'hostname', _('Device name'),
			_('Name shown in the device list. Leave empty to use the router\'s name.'));

		o = s.option(form.Value, 'exit_node', _('Exit node'),
			_('Where tunneled traffic leaves to the internet. Pick one or type its name or 100.x address.'));
		o.value('', _('None (normal internet)'));
		exitNodes.forEach(function(n) {
			o.value(n.ip, n.status ? '%s (%s, %s)'.format(n.name, n.ip, n.status) : '%s (%s)'.format(n.name, n.ip));
		});

		o = s.option(form.ListValue, 'mode', _('Use the exit node for'));
		o.value('all', _('All devices on the LAN'));
		o.value('clients', _('Chosen devices or networks'));
		o.value('domains', _('Chosen websites and services (any device)'));
		o.value('none', _('Nothing (router only joins the VPN)'));
		o.default = 'all';

		o = s.option(form.DynamicList, 'client_mac', _('Devices'),
			_('Devices whose traffic uses the exit node.'));
		o.datatype = 'macaddr';
		o.depends('mode', 'clients');
		if (hosts) {
			hosts.getMACHints(true).forEach(function(h) {
				o.value(h[0], h[1] ? '%s (%s)'.format(h[1], h[0]) : h[0]);
			});
		}

		o = s.option(form.DynamicList, 'client_zone', _('Networks'),
			_('Firewall zones whose devices all use the exit node, e.g. guest for a dedicated Wi-Fi.'));
		o.depends('mode', 'clients');

		o = s.option(form.DynamicList, 'domain', _('Websites'),
			_('Sites whose connections use the exit node; each covers its subdomains (espn.com covers www.espn.com).'));
		o.datatype = 'hostname';
		o.depends('mode', 'domains');

		o = s.option(form.Flag, 'dns_via_exit', _('Look up these sites through the exit node'),
			_('So the sites see the exit node\'s location when choosing a server.'));
		o.default = '1';
		o.depends('mode', 'domains');

		o = s.option(form.Flag, 'block_ipv6', _('Block IPv6 for tunneled traffic'),
			_('The tunnel carries IPv4 for LAN devices; blocking IPv6 makes apps switch to IPv4 right away.'));
		o.default = '1';
		o.depends('mode', 'all');
		o.depends('mode', 'clients');
		o.depends('mode', 'domains');

		o = s.option(form.Flag, 'advertise_exit_node', _('Offer this router as an exit node'),
			_('Lets other VPN devices send their internet traffic out through this router. ' +
			  'A router offering itself as an exit node cannot use another one.'));

		o = s.option(form.Value, 'port', _('Port'), _('UDP port for peer-to-peer traffic.'));
		o.datatype = 'port';
		o.placeholder = '41642';

		return m.render();
	}
});
