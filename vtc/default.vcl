# specify the VCL syntax version to use
vcl 4.1;

# import vmod_dynamic for better backend name resolution
import dynamic;
import std;

# we won't use any static backend, but Varnish still need a default one
backend default none;

# set up a dynamic director
# for more info, see https://github.com/nigoroll/libvmod-dynamic/blob/master/src/vmod_dynamic.vcc
sub vcl_init {
        new d = dynamic.director(port = "80");
}

sub vcl_recv {
        if (req.method == "BAN") {
                # Same ACL check as above:
                if (std.ban("req.http.host == " + req.http.host +
                    " && req.url == " + req.url)) {
                        return(synth(200, "Ban added"));
                } else {
                        # return ban error in 400 response
                        return(synth(400, std.ban_error()));
                }
        }
	# force the host header to match the backend (not all backends need it,
	# but example.com does)
	set req.http.host = "example.com";
	# set the backend
	set req.backend_hint = d.backend("example.com");
}
